package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/ops"
	"github.com/phansen314/koan/internal/schematest"
)

// The command table, the commands in ops, and the input schemas must name
// the same commands and the same fields, but for the CLI conveniences; and
// every command has an output schema.
func TestTableMatchesOpsAndSchemas(t *testing.T) {
	var names []string
	for _, c := range commands {
		names = append(names, c.name)
		file := filepath.Join(schematest.Dir(), strings.ReplaceAll(c.name, " ", "-"))
		if _, err := os.Stat(file + "-output.json"); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
		data, err := os.ReadFile(file + "-input.json")
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		var schema struct {
			Properties map[string]any `json:"properties"`
		}
		if err := json.Unmarshal(data, &schema); err != nil {
			t.Fatal(err)
		}
		fields := []string{}
		if c.subject != "" {
			fields = append(fields, c.subject)
		}
		for _, o := range c.options {
			if o.name != "notes-file" {
				fields = append(fields, strings.ReplaceAll(o.name, "-", "_"))
			}
		}
		var want []string
		for f := range schema.Properties {
			want = append(want, f)
		}
		slices.Sort(fields)
		slices.Sort(want)
		if c.name != "batch" && !slices.Equal(fields, want) {
			t.Errorf("%s: the command line sets %v, the input schema has %v", c.name, fields, want)
		}
	}
	slices.Sort(names)
	if got := ops.Names(); !slices.Equal(names, got) {
		t.Errorf("command line has %v, ops has %v", names, got)
	}
}

// buildInput is checked through a command that records what it was given.
func TestBuildInput(t *testing.T) {
	cases := []struct {
		args []string
		want string // "" for a usage error
	}{
		{[]string{"task", "show", "42"}, `{"id":42}`},
		{[]string{"task", "show", "abc"}, `{"id":"abc"}`},
		{[]string{"task", "show", "@42"}, `{"id":"@42"}`},
		{[]string{"task", "block", "@1", "--blocked-by", "2,@3"}, `{"blocked_by":[2,"@3"],"id":"@1"}`},
		{[]string{"task", "list", "--folder", "41"}, `{"folder":"41"}`},
		{[]string{"where", "@41:docs/plan.md"}, `{"ref":"@41:docs/plan.md"}`},
		{[]string{"where", "--file", "/a/../b"}, `{"file":"/b"}`},
		{[]string{"where"}, `{}`},
		{[]string{"where", "a", "b"}, ""},
		{[]string{"task", "create", "Book flights", "--priority", "2", "--tags", "a,b", "--tags", "c"}, `{"priority":2,"tags":["a","b","c"],"title":"Book flights"}`},
		{[]string{"task", "update", "1", "--priority", "null", "--extra-remove", "a,b", "--extra-remove", "c"}, `{"extra_remove":["a,b","c"],"id":1,"priority":null}`},
		{[]string{"task", "update", "1", "--extra-merge", `{"n":1.50}`}, `{"extra_merge":{"n":1.50},"id":1}`},
		{[]string{"task", "update", "1", "--extra-merge", "{"}, `{"extra_merge":"{","id":1}`},
		{[]string{"task", "claim", "1", "--extra", "nope"}, `{"extra":"nope","id":1}`},
		{[]string{"task", "block", "1", "--blocked-by", "2,x"}, `{"blocked_by":[2,"x"],"id":1}`},
		{[]string{"repair", "--kinds", "temp-leftover,stale-claim", "--kinds", "orphan-notes"}, `{"kinds":["temp-leftover","stale-claim","orphan-notes"]}`},
		{[]string{"task", "create", "a", "--blocks", "1,2"}, `{"blocks":[1,2],"title":"a"}`},
		{[]string{"folder", "update", "/p", "--title", "null"}, `{"folder":"/p","title":null}`},
		{[]string{"folder", "create", "/p", "--title", "P", "--extra", `{"k":1}`}, `{"extra":{"k":1},"folder":"/p","title":"P"}`},
		{[]string{"folder", "delete", "/p", "--contents", "--force"}, `{"contents":true,"folder":"/p","force":true}`},
		{[]string{"folder", "list", "--folder", "/p", "--recursive=false", "--limit", "2", "--fields", "title"}, `{"fields":["title"],"folder":"/p","limit":2,"recursive":false}`},
		{[]string{"task", "list", "--recursive=false", "--claimed", "--limit", "5"}, `{"claimed":true,"limit":5,"recursive":false}`},
		{[]string{"task", "create", "--priority", "-3", "--", "-x"}, `{"priority":-3,"title":"-x"}`},
		{[]string{"task", "create", "a", "b"}, ""},
		{[]string{"task", "show"}, ""},
		{[]string{"init", "extra"}, ""},
		{[]string{"task", "list", "--recursive", "false"}, ""},
		{[]string{"task", "show", "1", "--input", "f"}, ""},
		{[]string{"task", "create", "a", "--notes", "n", "--notes-file", "f"}, ""},
		{[]string{"task", "list", "--tag", "x"}, ""},
		{[]string{"folder", "delete", "/p", "-r"}, ""},
		{[]string{"task"}, ""},
		{[]string{"task", "frob"}, ""},
		{[]string{"show", "1"}, ""},
	}
	// A relative --file is taken from the working directory.
	wd, _ := os.Getwd()
	cases = append(cases, struct {
		args []string
		want string
	}{[]string{"where", "--file", "docs/plan.md"}, `{"file":"` + wd + `/docs/plan.md"}`})
	for _, c := range cases {
		var got []byte
		var failed error
		root := newRoot(Env{}, new(*ops.Envelope))
		var record func(*cobra.Command)
		record = func(parent *cobra.Command) {
			for _, sub := range parent.Commands() {
				if sub.HasSubCommands() {
					record(sub)
					continue
				}
				spec := specOf(strings.TrimPrefix(sub.CommandPath(), "koan "))
				sub.RunE = func(cmd *cobra.Command, args []string) error {
					got, failed = buildInput(spec, cmd, args, Env{Stdin: bytes.NewReader(nil)})
					return failed
				}
			}
		}
		record(root)
		root.SetArgs(c.args)
		root.SetOut(new(bytes.Buffer))
		root.SetErr(new(bytes.Buffer))
		err := root.Execute()
		switch {
		case c.want == "" && err == nil:
			t.Errorf("koan %v: built %s, want a usage error", c.args, got)
		case c.want != "" && (err != nil || string(got) != c.want):
			t.Errorf("koan %v: built %s (%v), want %s", c.args, got, err, c.want)
		}
	}
}

// A command line koan can't understand is usage, exit 2; a file it can't
// read is io, exit 1. Neither reaches the command.
func TestRunErrors(t *testing.T) {
	for _, c := range []struct {
		args []string
		kind errs.Kind
		code int
	}{
		{[]string{"task"}, errs.Usage, ExitUsage},
		{[]string{"folder", "frob"}, errs.Usage, ExitUsage},
		{[]string{"task", "create", "a", "--notes-file", filepath.Join(t.TempDir(), "none")}, errs.IO, ExitError},
		{[]string{"batch", "-i", t.TempDir()}, errs.IO, ExitError},
	} {
		var out bytes.Buffer
		code := Run(c.args, Env{Stdin: bytes.NewReader(nil), Stdout: &out, Stderr: new(bytes.Buffer)})
		var env struct {
			Error struct{ Kind errs.Kind }
		}
		json.Unmarshal(out.Bytes(), &env)
		if code != c.code || env.Error.Kind != c.kind {
			t.Errorf("koan %v: exit %d, %s; want %d, %s", c.args, code, out.Bytes(), c.code, c.kind)
		}
	}
}

func specOf(name string) command {
	for _, c := range commands {
		if c.name == name {
			return c
		}
	}
	return command{}
}

// Help lists no help command, which is an unknown command; names each
// option's kind, not how it is read; and gives a command's required
// options in its usage line.
func TestHelp(t *testing.T) {
	help := func(args ...string) string {
		var out bytes.Buffer
		if code := Run(append(args, "--help"), Env{Stdin: bytes.NewReader(nil), Stdout: &out, Stderr: new(bytes.Buffer)}); code != ExitOK {
			t.Fatalf("koan %v --help: exit %d", args, code)
		}
		return out.String()
	}
	if h := help(); strings.Contains(h, "\n  help ") || strings.Contains(h, "[command]") || strings.Contains(h, "Flags:") {
		t.Errorf("koan --help:\n%s", h)
	}
	h := help("task", "list")
	for _, want := range []string{"--recursive  ", "--limit int ", "--fields list ", "(default ready,blocked; comma-separated, repeatable)"} {
		if !strings.Contains(h, want) {
			t.Errorf("koan task list --help lacks %q:\n%s", want, h)
		}
	}
	if strings.Contains(h, "string[=") || strings.Contains(h, "stringArray") {
		t.Errorf("koan task list --help names how options are read:\n%s", h)
	}
	// A file koan reads, or - for stdin, is a file; where's --file a path.
	if h := help("task", "create"); !strings.Contains(h, "--notes-file file ") || !strings.Contains(h, "--input file ") {
		t.Errorf("koan task create --help: the options that read a file:\n%s", h)
	}
	if h := help("where"); !strings.Contains(h, "--file path ") {
		t.Errorf("koan where --help:\n%s", h)
	}
	for _, c := range [][]string{{"task", "move"}, {"folder", "move"}, {"task", "block"}, {"task", "unblock"}, {"folder", "block"}, {"folder", "unblock"}} {
		want := map[string]string{"move": "--to <folder>", "block": "--blocked-by <blockers>", "unblock": "--blocked-by <blockers>"}[c[1]]
		if h := help(c...); !strings.Contains(h, "koan "+strings.Join(c, " ")+" <") || !strings.Contains(h, want+" [options]") {
			t.Errorf("koan %v --help: usage line lacks %s:\n%s", c, want, h)
		}
	}
}
