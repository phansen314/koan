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

	"github.com/phansen314/koan/internal/ops"
	"github.com/phansen314/koan/internal/schematest"
)

// The command table, the commands in ops, and the input schemas must name
// the same commands and the same fields.
func TestTableMatchesOpsAndSchemas(t *testing.T) {
	var names []string
	for _, c := range commands {
		names = append(names, c.name)
		data, err := os.ReadFile(filepath.Join(schematest.Dir(), c.name+"-input.json"))
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
		if c.name != "create-batch" && !slices.Equal(fields, want) {
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
		{[]string{"show", "42"}, `{"id":42}`},
		{[]string{"show", "abc"}, `{"id":"abc"}`},
		{[]string{"create", "Book flights", "--priority", "2", "--tags", "a,b", "--tags", "c"}, `{"priority":2,"tags":["a","b","c"],"title":"Book flights"}`},
		{[]string{"update", "1", "--priority", "null", "--extra-remove", "a,b", "--extra-remove", "c"}, `{"extra_remove":["a,b","c"],"id":1,"priority":null}`},
		{[]string{"update", "1", "--extra-merge", `{"n":1.50}`}, `{"extra_merge":{"n":1.50},"id":1}`},
		{[]string{"block", "1", "--blockers", "2,x"}, `{"blockers":[2,"x"],"id":1}`},
		{[]string{"list", "--recursive=false", "--claimed", "--limit", "5"}, `{"claimed":true,"limit":5,"recursive":false}`},
		{[]string{"create", "--priority", "-3", "--", "-x"}, `{"priority":-3,"title":"-x"}`},
		{[]string{"create", "a", "b"}, ""},
		{[]string{"init", "extra"}, ""},
		{[]string{"list", "--recursive", "false"}, ""},
		{[]string{"show", "1", "--input", "f"}, ""},
		{[]string{"update", "1", "--extra-merge", "{"}, ""},
		{[]string{"list", "--tag", "x"}, ""},
	}
	for _, c := range cases {
		var got []byte
		var failed error
		root := newRoot(Env{}, new(*ops.Envelope))
		for _, sub := range root.Commands() {
			spec := specOf(sub.Name())
			sub.RunE = func(cmd *cobra.Command, args []string) error {
				got, failed = buildInput(spec, cmd, args, Env{Stdin: bytes.NewReader(nil)})
				return failed
			}
		}
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

func specOf(name string) command {
	for _, c := range commands {
		if c.name == name {
			return c
		}
	}
	return command{}
}
