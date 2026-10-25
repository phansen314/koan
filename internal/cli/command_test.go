package cli

import (
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
)

// fake has one option of every kind, so the command-line rules can be
// tested before the commands that use them exist.
var fake = &Command{
	Name:    "thing make",
	Subject: &Option{Field: "title", Required: true},
	Options: []Option{
		{Field: "priority", Kind: Integer, Nullable: true},
		{Field: "limit", Kind: Integer},
		{Field: "note", Kind: String, Nullable: true},
		{Field: "name", Kind: String},
		{Field: "contents", Kind: Bool},
		{Field: "tags", Kind: List},
		{Field: "extra_remove", Kind: Each},
		{Field: "extra", Kind: JSON},
	},
	Files: []File{{Name: "--note-file", Field: "note"}},
	Run:   func(env *Env, in Input) (any, []Warning, *Error) { return in, nil, nil },
}

// optional takes a subject or none.
var optional = &Command{
	Name:    "where",
	Subject: &Option{Field: "ref"},
	Run:     func(env *Env, in Input) (any, []Warning, *Error) { return in, nil, nil },
}

// join takes a subject, then a list as its object.
var join = &Command{
	Name:    "task join",
	Subject: &Option{Field: "task", Required: true},
	Object:  &Option{Field: "join", Kind: List, Arg: "folder", Required: true},
	Run:     func(env *Env, in Input) (any, []Warning, *Error) { return in, nil, nil },
}

// move takes a list as its subject, then one object, or -t in the
// object's place.
var move = &Command{
	Name:    "task mv",
	Subject: &Option{Field: "tasks", Kind: List, Arg: "task", Required: true},
	Object:  &Option{Field: "into", Arg: "folder", Required: true},
	Instead: "into",
	Options: []Option{{Field: "into", Short: "t"}},
	Run:     func(env *Env, in Input) (any, []Warning, *Error) { return in, nil, nil },
}

// rename is move with -T as well, which makes the subject one word and
// can't go with -t.
var rename = &Command{
	Name:    "folder mv",
	Subject: &Option{Field: "folders", Kind: List, Arg: "folder", Required: true},
	Object:  &Option{Field: "dest", Required: true},
	Instead: "into",
	Single:  "rename",
	Apart:   []string{"into", "rename"},
	Options: []Option{{Field: "into", Short: "t"}, {Field: "rename", Short: "T", Kind: Bool}},
	Run:     func(env *Env, in Input) (any, []Warning, *Error) { return in, nil, nil },
}

// bind takes a subject, then a directory it can do without.
var bind = &Command{
	Name:    "marker bind",
	Subject: &Option{Field: "folder", Required: true},
	Object:  &Option{Field: "dir", Kind: Path},
	Run:     func(env *Env, in Input) (any, []Warning, *Error) { return in, nil, nil },
}

// scope takes the folder it looks under, or none.
var scope = &Command{
	Name:    "task list",
	Subject: &Option{Field: "folder"},
	Options: []Option{{Field: "recursive", Kind: Bool}},
	Run:     func(env *Env, in Input) (any, []Warning, *Error) { return in, nil, nil },
}

// bare takes nothing.
var bare = &Command{
	Name: "version",
	Run:  func(env *Env, in Input) (any, []Warning, *Error) { return in, nil, nil },
}

// parse reads the arguments after a command's name, through the tree
// Main runs, with c the only command.
func (c *Command) parse(args []string) (*line, *Error) {
	var got *line
	root, _ := tree([]*Command{c}, io.Discard, &Env{}, false, func(_ *Command, l *line) { got = l })
	root.SetErr(io.Discard)
	root.SetArgs(append(strings.Fields(c.Name), args...))
	if err := root.Execute(); err != nil {
		return nil, usage(err)
	}
	if got == nil {
		return nil, Usage("", "help, not a command line")
	}
	return got, nil
}

func parseJSON(t *testing.T, s string) Input {
	t.Helper()
	v, err := decodeOne([]byte(s))
	if err != nil {
		t.Fatalf("bad want %q: %v", s, err)
	}
	return Input(v.(map[string]any))
}

func TestParseLines(t *testing.T) {
	cases := []struct {
		cmd  *Command
		args []string
		want string
	}{
		{fake, []string{"Book flights"}, `{"title":"Book flights"}`},
		{fake, []string{"--", "-x"}, `{"title":"-x"}`},
		{fake, []string{"--", "--priority"}, `{"title":"--priority"}`},
		{fake, []string{"-"}, `{"title":"-"}`},
		{fake, []string{"null"}, `{"title":"null"}`},
		{fake, []string{"t", "--priority", "2"}, `{"title":"t","priority":2}`},
		{fake, []string{"t", "--priority=-3"}, `{"title":"t","priority":-3}`},
		{fake, []string{"t", "--priority", "-3"}, `{"title":"t","priority":-3}`},
		{fake, []string{"t", "--priority", "null"}, `{"title":"t","priority":null}`},
		{fake, []string{"t", "--priority", "99999999999999999999"}, `{"title":"t","priority":99999999999999999999}`},
		{fake, []string{"t", "--priority", "0"}, `{"title":"t","priority":0}`},
		{fake, []string{"t", "--note", "null"}, `{"title":"t","note":null}`},
		{fake, []string{"t", "--name", "null"}, `{"title":"t","name":"null"}`},
		{fake, []string{"t", "--contents"}, `{"title":"t","contents":true}`},
		{fake, []string{"t", "--contents=false"}, `{"title":"t","contents":false}`},
		{fake, []string{"t", "--contents=true"}, `{"title":"t","contents":true}`},
		{fake, []string{"t", "--tags", "a,b"}, `{"title":"t","tags":["a","b"]}`},
		{fake, []string{"t", "--tags", "a", "--tags", "b"}, `{"title":"t","tags":["a","b"]}`},
		{fake, []string{"t", "--tags", "a,b", "--tags=c"}, `{"title":"t","tags":["a","b","c"]}`},
		{fake, []string{"t", "--tags", "null"}, `{"title":"t","tags":["null"]}`},
		{fake, []string{"t", "--extra-remove", "a,b", "--extra-remove", "c"}, `{"title":"t","extra_remove":["a,b","c"]}`},
		{fake, []string{"t", "--extra", `{"a":[1,2]}`}, `{"title":"t","extra":{"a":[1,2]}}`},
		{fake, []string{"t", "--extra", `[1]`}, `{"title":"t","extra":[1]}`},
		{fake, []string{"t", "--extra", `{"a":`}, `{"title":"t","extra":"{\"a\":"}`},
		{fake, []string{"t", "--extra", `{} {}`}, `{"title":"t","extra":"{} {}"}`},
		{fake, []string{"--name", "n", "t"}, `{"title":"t","name":"n"}`},
		{fake, []string{"t", "--name", "-n"}, `{"title":"t","name":"-n"}`},
		{fake, []string{"t", "--name="}, `{"title":"t","name":""}`},
		{optional, nil, `{}`},
		{optional, []string{"@41:docs/plan.md"}, `{"ref":"@41:docs/plan.md"}`},
		{bare, nil, `{}`},
		{join, []string{"42", "/a"}, `{"task":"42","join":["/a"]}`},
		{join, []string{"42", "/a,/b"}, `{"task":"42","join":["/a","/b"]}`},
		{join, []string{"42", "/a", "/b,/c"}, `{"task":"42","join":["/a","/b","/c"]}`},
		{move, []string{"42", "/w"}, `{"tasks":["42"],"into":"/w"}`},
		{move, []string{"41", "42", "/w"}, `{"tasks":["41","42"],"into":"/w"}`},
		{move, []string{"41,42", "/w"}, `{"tasks":["41","42"],"into":"/w"}`},
		{move, []string{"41", "42,43", "/w,x"}, `{"tasks":["41","42","43"],"into":"/w,x"}`},
		{move, []string{"-t", "/w", "42"}, `{"tasks":["42"],"into":"/w"}`},
		{move, []string{"--into", "/w", "41", "42"}, `{"tasks":["41","42"],"into":"/w"}`},
		{move, []string{"41", "-t=/w", "42,43"}, `{"tasks":["41","42","43"],"into":"/w"}`},
		{rename, []string{"/a", "/b"}, `{"folders":["/a"],"dest":"/b"}`},
		{rename, []string{"/a", "/b,/c", "/d"}, `{"folders":["/a","/b","/c"],"dest":"/d"}`},
		{rename, []string{"-t", "/d", "/a", "/b"}, `{"folders":["/a","/b"],"into":"/d"}`},
		{rename, []string{"-T", "/a", "/b"}, `{"folders":["/a"],"dest":"/b","rename":true}`},
		{rename, []string{"/a", "--rename", "/b,c"}, `{"folders":["/a"],"dest":"/b,c","rename":true}`},
		{rename, []string{"-T", "/a,/b", "/c"}, `{"folders":["/a","/b"],"dest":"/c","rename":true}`},
		{rename, []string{"--rename=false", "/a", "/b", "/c"}, `{"folders":["/a","/b"],"dest":"/c","rename":false}`},
		{bind, []string{"@41"}, `{"folder":"@41"}`},
		{bind, []string{"@41", "/d"}, `{"folder":"@41","dir":"/d"}`},
		{bind, []string{"@41", "/d,e"}, `{"folder":"@41","dir":"/d,e"}`},
		{scope, nil, `{}`},
		{scope, []string{"/w"}, `{"folder":"/w"}`},
		{scope, []string{"/w,x", "--recursive=false"}, `{"folder":"/w,x","recursive":false}`},
	}
	for _, c := range cases {
		l, e := c.cmd.parse(c.args)
		if e != nil {
			t.Errorf("%s %q: %s: %s", c.cmd.Name, c.args, e.Kind, e.Message)
			continue
		}
		if l.hasInput {
			t.Errorf("%s %q: read as --input", c.cmd.Name, c.args)
		}
		if want := parseJSON(t, c.want); !reflect.DeepEqual(l.in, want) {
			got, _ := json.Marshal(l.in)
			t.Errorf("%s %q:\n got %s\nwant %s", c.cmd.Name, c.args, got, c.want)
		}
	}
}

func TestParseInputOption(t *testing.T) {
	for _, args := range [][]string{{"-i", "f.json"}, {"--input", "f.json"}, {"--input=f.json"}, {"-i=f.json"}, {"-if.json"}} {
		l, e := fake.parse(args)
		if e != nil || !l.hasInput || l.input != "f.json" || len(l.in) != 0 {
			t.Errorf("%q: got %+v, %v", args, l, e)
		}
	}
	l, e := fake.parse([]string{"-i", "-"})
	if e != nil || l.input != "-" {
		t.Errorf("-i -: got %+v, %v", l, e)
	}
}

// TestParseUsage is the table of command lines that can't be turned into
// an input. Each is usage, naming what is at fault: an option by its long
// name, an unknown short option, an extra subject as typed, or "".
func TestParseUsage(t *testing.T) {
	cases := []struct {
		cmd   *Command
		args  []string
		field string
	}{
		{fake, nil, ""},
		{fake, []string{"--priority", "2"}, ""},
		{fake, []string{"a", "b"}, "b"},
		{fake, []string{"a", "--", "b"}, "b"},
		{fake, []string{"t", "--frob"}, "--frob"},
		{fake, []string{"t", "--frob=1"}, "--frob"},
		{fake, []string{"t", "-x"}, "-x"},
		{fake, []string{"t", "-xi", "f"}, "-x"},
		{fake, []string{"t", "---x"}, "---x"},
		{fake, []string{"t", "--=x"}, "--=x"},
		{fake, []string{"t", "--Priority", "1"}, "--Priority"},
		{fake, []string{"t", "--prio", "1"}, "--prio"},
		{fake, []string{"t", "--extra_remove", "k"}, "--extra_remove"},
		{fake, []string{"t", "--priority"}, "--priority"},
		{fake, []string{"t", "--tags"}, "--tags"},
		{fake, []string{"t", "--priority", "1", "--priority", "2"}, "--priority"},
		{fake, []string{"t", "--priority", "high"}, "--priority"},
		{fake, []string{"t", "--priority", "+5"}, "--priority"},
		{fake, []string{"t", "--priority", "007"}, "--priority"},
		{fake, []string{"t", "--priority", "1e3"}, "--priority"},
		{fake, []string{"t", "--priority="}, "--priority"},
		{fake, []string{"t", "--limit", "null"}, "--limit"},
		{fake, []string{"t", "--contents=yes"}, "--contents"},
		{fake, []string{"t", "--contents=null"}, "--contents"},
		{fake, []string{"t", "--contents", "--contents=false"}, "--contents"},
		{fake, []string{"t", "--contents", "false"}, "false"},
		{fake, []string{"t", "--name", "a", "--name=b"}, "--name"},
		{fake, []string{"t", "--extra", "{}", "--extra", "{}"}, "--extra"},
		{fake, []string{"-i"}, "--input"},
		{fake, []string{"--input"}, "--input"},
		{fake, []string{"-i", "f", "t"}, "t"},
		{fake, []string{"t", "-i", "f"}, "t"},
		{fake, []string{"-i", "f", "--name", "n"}, "--name"},
		{fake, []string{"-i", "f", "--tags", "a", "--contents"}, "--contents"},
		{fake, []string{"-i", "f", "-i", "g"}, "--input"},
		{fake, []string{"-i", "f", "--input=g"}, "--input"},
		{fake, []string{"-i", "f", "--", "t"}, "t"},
		{fake, []string{"t", "--note", "n", "--note-file", "f"}, ""},
		{fake, []string{"t", "--note-file", "f", "--note", "n"}, ""},
		{fake, []string{"t", "--note-file", "f", "--note-file", "g"}, "--note-file"},
		{fake, []string{"t", "--note-file"}, "--note-file"},
		{fake, []string{"-i", "f", "--note-file", "g"}, "--note-file"},
		{optional, []string{"a", "b"}, "b"},
		{bare, []string{"x"}, "x"},
		{bare, []string{"--", "x"}, "x"},
		{join, nil, ""},
		{join, []string{"42"}, ""},
		{join, []string{"-i", "f", "42"}, "42"},
		{join, []string{"42", "/a", "--join", "/b"}, "--join"},
		{move, nil, ""},
		{move, []string{"42"}, ""},
		{move, []string{"-t", "/w"}, ""},
		{move, []string{"-t", "/w", "-t", "/x", "42"}, "--into"},
		{move, []string{"-i", "f", "42", "/w"}, "42"},
		{move, []string{"-i", "f", "-t", "/w"}, "--into"},
		{rename, []string{"/a"}, ""},
		{rename, []string{"-T", "/a"}, ""},
		{rename, []string{"-T", "/a", "/b", "/c"}, "/c"},
		{rename, []string{"-T", "-t", "/c", "/a"}, ""},
		{rename, []string{"-t", "/c", "/a", "-T"}, ""},
		{rename, []string{"/a", "/b", "--to", "/c"}, "--to"},
		{bind, nil, ""},
		{bind, []string{"@41", "/d", "/e"}, "/e"},
		{bind, []string{"@41", "--dir", "/d"}, "--dir"},
		{scope, []string{"/a", "/b"}, "/b"},
		{scope, []string{"--folder", "/a"}, "--folder"},
		{scope, []string{"-i", "f", "/a"}, "/a"},
	}
	for _, c := range cases {
		_, e := c.cmd.parse(c.args)
		if e == nil {
			t.Errorf("%s %q: parsed, want usage", c.cmd.Name, c.args)
			continue
		}
		if e.Kind != KindUsage || len(e.Problems) != 1 || e.Problems[0].Field != c.field {
			t.Errorf("%s %q: got %s %+v, want usage at %q", c.cmd.Name, c.args, e.Kind, e.Problems, c.field)
		}
	}
}

// A convenience names the file its field is read from; the field is set
// only once the file is read.
func TestParseFiles(t *testing.T) {
	for _, args := range [][]string{{"t", "--note-file", "n.md"}, {"t", "--note-file=n.md"}, {"--note-file", "n.md", "t"}} {
		l, e := fake.parse(args)
		if e != nil || len(l.in) != 1 || l.files["note"] != "n.md" {
			t.Errorf("%q: got %+v, %v", args, l, e)
		}
	}
}

// TestLookup finds the command a line names, by one word or two, matched
// exactly; a line that names none is usage.
func TestLookup(t *testing.T) {
	cmds := []*Command{fake, optional, bare}
	cases := []struct {
		args  []string
		ok    bool
		field string // for usage
	}{
		{[]string{"version"}, true, ""},
		{[]string{"thing", "make", "t"}, true, ""},
		{[]string{"where", "x"}, true, ""},
		{nil, false, ""},
		{[]string{}, false, ""},
		{[]string{"frob"}, false, "frob"},
		{[]string{"Version"}, false, "Version"},
		{[]string{"vers"}, false, "vers"},
		{[]string{"thing"}, false, ""},
		{[]string{"thing", "frob"}, false, "frob"},
		{[]string{"thing", "Make", "t"}, false, "Make"},
		{[]string{"make"}, false, "make"},
		{[]string{"--input", "f"}, false, "--input"},
		{[]string{"-i", "f"}, false, "-i"},
		{[]string{"--frob", "version"}, false, "--frob"},
	}
	for _, c := range cases {
		r := runMain(t, "", cmds, c.args...)
		var env struct {
			OK    bool
			Error struct {
				Kind     string
				Problems []Problem
			}
		}
		if err := json.Unmarshal([]byte(r.stdout), &env); err != nil {
			t.Errorf("%q: not an envelope: %q", c.args, r.stdout)
			continue
		}
		switch {
		case c.ok && (!env.OK || r.code != 0):
			t.Errorf("%q: got %d %s", c.args, r.code, r.stdout)
		case !c.ok && (r.code != ExitUsage || env.Error.Kind != KindUsage || len(env.Error.Problems) != 1 || env.Error.Problems[0].Field != c.field):
			t.Errorf("%q: got %d %s; want usage at %q", c.args, r.code, r.stdout, c.field)
		}
	}
}

func TestUnknownFields(t *testing.T) {
	if e := fake.unknownFields(Input{"title": "t", "tags": []any{}}); e != nil {
		t.Errorf("known fields refused: %+v", e)
	}
	e := bare.unknownFields(Input{"b": 1, "a/b": 2})
	if e == nil || e.Kind != KindInvalidInput {
		t.Fatalf("got %+v, want invalid-input", e)
	}
	want := []Problem{{"/a~1b", "unknown field"}, {"/b", "unknown field"}}
	if !reflect.DeepEqual(e.Problems, want) {
		t.Errorf("problems %+v, want %+v", e.Problems, want)
	}
}
