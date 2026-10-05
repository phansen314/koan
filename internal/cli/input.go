package cli

import (
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/jsonio"
	"github.com/phansen314/ftask/internal/ops"
)

// integer is a JSON integer literal: what Int converts to a number.
var integer = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

// buildInput places each given argument and option at its field. Values are
// judged by the adapters; the problems returned are the CLI's own —
// non-UTF-8 values and JSON option values that cannot be read — and a field
// with one is left out of the input (implementation-spec.md, Conversion). A
// TextFile option's file that can't be read is the error that stops the
// command.
func buildInput(c *Command, cmd *cobra.Command, args []string, env Env) (*jsonio.Object, []errs.Problem, *errs.Error) {
	in := &jsonio.Object{}
	var problems []errs.Problem
	bad := func(field, reason string) { problems = append(problems, errs.Problem{Field: field, Reason: reason}) }

	for i, a := range c.Args {
		if !utf8.ValidString(args[i]) {
			bad(a.Field, "must be UTF-8")
			continue
		}
		setAt(in, a.Field, scalar(a.Type, args[i]))
	}
	for _, o := range c.Options {
		f := cmd.Flags().Lookup(o.Name)
		if !f.Changed || o.Field == "" {
			continue
		}
		var raw []string
		switch o.Type {
		case Bool:
			b, _ := cmd.Flags().GetBool(o.Name)
			setAt(in, o.Field, b)
			continue
		case IDList, StringList, Repeated:
			raw, _ = cmd.Flags().GetStringArray(o.Name)
		case TextFile:
			name, _ := cmd.Flags().GetString(o.Name)
			data, e := readText(name, env)
			switch {
			case e != nil:
				return nil, nil, e
			case !utf8.Valid(data):
				bad(o.Field, "must be UTF-8")
			default:
				setAt(in, o.Field, string(data))
			}
			continue
		case EnvelopeFile:
			name, _ := cmd.Flags().GetString(o.Name)
			data, e := readText(name, env)
			if e != nil {
				return nil, nil, e
			}
			if ids, reason := ops.EnvelopeIDs(data); reason != "" {
				bad(o.Field, reason)
			} else {
				setAt(in, o.Field, ids)
			}
			continue
		default:
			s, _ := cmd.Flags().GetString(o.Name)
			raw = []string{s}
		}
		if !allUTF8(raw) {
			bad(o.Field, "must be UTF-8")
			continue
		}
		switch o.Type {
		case IDList, StringList:
			items := []any{}
			for _, s := range raw {
				if s == "" {
					continue
				}
				for _, item := range strings.Split(s, ",") {
					items = append(items, scalar(itemType(o.Type), item))
				}
			}
			setAt(in, o.Field, items)
		case Repeated:
			items := make([]any, len(raw))
			for i, s := range raw {
				items[i] = s
			}
			setAt(in, o.Field, items)
		case JSON:
			v, repeated, err := jsonio.ParseValueAt([]byte(raw[0]), depth(o.Field))
			switch {
			case err != nil:
				bad(o.Field, "not valid JSON: "+err.Error())
			case len(repeated) > 0:
				for _, r := range repeated {
					bad(o.Field+r, "repeated key")
				}
			default:
				setAt(in, o.Field, v)
			}
		default:
			setAt(in, o.Field, scalar(o.Type, raw[0]))
		}
	}
	return in, problems, nil
}

func itemType(t Type) Type {
	if t == IDList {
		return Int
	}
	return String
}

// scalar converts one token of type t.
func scalar(t Type, s string) any {
	switch {
	case t == NullableInt && s == "null":
		return nil
	case (t == Int || t == NullableInt) && integer.MatchString(s):
		return json.Number(s)
	}
	return s
}

func allUTF8(ss []string) bool {
	for _, s := range ss {
		if !utf8.ValidString(s) {
			return false
		}
	}
	return true
}

// depth is how many levels deep ptr's field sits in the input: 1 for /extra.
func depth(ptr string) int { return strings.Count(ptr, "/") }

// setAt sets the field at ptr, creating the objects above it. Pointers come
// from the command tables: plain names, no escapes.
func setAt(obj *jsonio.Object, ptr string, v any) {
	segs := strings.Split(strings.TrimPrefix(ptr, "/"), "/")
	for _, s := range segs[:len(segs)-1] {
		next, ok := obj.Get(s)
		child, isObj := next.(*jsonio.Object)
		if !ok || !isObj {
			child = &jsonio.Object{}
			obj.Set(s, child)
		}
		obj = child
	}
	obj.Set(segs[len(segs)-1], v)
}

// readInput reads --input: the file at path, or stdin for "-". It returns
// the input object, or the error that stops the command — io if it cannot
// be read, invalid-input if it cannot be read as one JSON object.
func readInput(path string, env Env) (*jsonio.Object, *errs.Error) {
	data, e := readText(path, env)
	if e != nil {
		return nil, e
	}
	obj, repeated, err := jsonio.ParseObject(data)
	if err != nil {
		return nil, errs.InvalidInput([]errs.Problem{{Field: "", Reason: err.Error()}})
	}
	if len(repeated) > 0 {
		ps := make([]errs.Problem, len(repeated))
		for i, r := range repeated {
			ps[i] = errs.Problem{Field: r, Reason: "repeated key"}
		}
		return nil, errs.InvalidInput(ps)
	}
	return obj, nil
}

// readText reads the file at path, or stdin for "-": io if it can't be read.
func readText(path string, env Env) ([]byte, *errs.Error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(env.Stdin)
	} else {
		data, err = env.Ops.FS.ReadFile(path)
	}
	if err != nil {
		return nil, errs.FromOS(path, err)
	}
	return data, nil
}

// resolveRoot resolves init's root, which the operation leaves to its caller
// (cli-spec.md, init): "~" and a leading "~/" expand into the home directory,
// "~user/" is refused, and a relative path is joined to the working
// directory. Nothing is cleaned, so a ".." segment reaches the operation,
// which rejects it. A root that is absent, empty, or not a string is left
// for the operation to judge.
func resolveRoot(in *jsonio.Object, env Env) ([]errs.Problem, *errs.Error) {
	v, _ := in.Get("root")
	s, ok := v.(string)
	switch {
	case !ok || s == "" || strings.HasPrefix(s, "/"):
		return nil, nil
	case s == "~" || strings.HasPrefix(s, "~/"):
		if env.Ops.Home == "" {
			return nil, errs.Environment("HOME")
		}
		s = env.Ops.Home + s[1:]
	case strings.HasPrefix(s, "~"):
		return []errs.Problem{{Field: "/root", Reason: "~user/ is not supported: use ~/ or an absolute path"}}, nil
	default:
		wd, err := env.Getwd()
		if err != nil {
			return nil, errs.FromOS(".", err)
		}
		s = strings.TrimSuffix(wd, "/") + "/" + s
	}
	in.Set("root", s)
	return nil, nil
}
