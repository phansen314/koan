package main_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func message(r interface{ Error() map[string]any }) string {
	m, _ := r.Error()["message"].(string)
	return m
}

// An option a sibling command takes gets a hint in its usage message,
// at the option; a spelling an earlier version took gets none.
func TestSiblingHints(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/w")
	for _, c := range []struct {
		args []string
		opt  string
		hint string
	}{
		{[]string{"task", "list", "--folder", "/w"}, "--folder", "the folder is a word of its own"},
		{[]string{"frontier", "--folder", "/w"}, "--folder", "the folder is a word of its own"},
		{[]string{"claim-next", "--folder", "/w"}, "--folder", "the folder is a word of its own"},
		{[]string{"folder", "list", "--folder", "/w"}, "--folder", "the folder is a word of its own"},
		{[]string{"context", "--folder", "/w"}, "--folder", "the folder is a word of its own"},
		{[]string{"export", "--folder", "/w"}, "--folder", "the folder is a word of its own"},
		{[]string{"join", "1", "--join", "/w"}, "--join", "name the folders to join after the task or folder"},
		{[]string{"update", "1", "--tags", "a"}, "--tags", "--tags-add, --tags-remove and --tags-replace-all"},
		{[]string{"task", "create", "t", "--tags-add", "a"}, "--tags-add", "task create takes --tags"},
		{[]string{"task", "create", "--title", "t"}, "--title", "the title is a word of its own"},
	} {
		r := usageAt(t, home, c.opt, c.args...)
		if m := message(r); !strings.Contains(m, "unknown flag: "+c.opt+"; ") || !strings.Contains(m, c.hint) {
			t.Errorf("koan %q: message %q lacks hint %q", c.args, m, c.hint)
		}
	}
	// No hint for an option no sibling takes, nor for the old spellings.
	for _, args := range [][]string{{"task", "list", "--frob"}, {"frontier", "--recursive"}, {"mv", "1", "--to", "/w"}} {
		r := koan(t, home, args...)
		if m := message(r); r.Kind() != "usage" || strings.Contains(m, ";") {
			t.Errorf("koan %q: %s", args, r.Line())
		}
	}
}

// A boolean's value as a word of its own is a positional, and when it
// fails there the message says to write it after =.
func TestBooleanValueWord(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/w")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"task", "list", "--claimed", "false"}, "--claimed=false"},
		{[]string{"task", "list", "--claimed", "null"}, "--claimed=null"},
		{[]string{"frontier", "--direct", "true"}, "--direct=true"},
	} {
		r := koan(t, home, c.args...)
		if r.Kind() != "invalid-input" || !strings.Contains(message(r), "a boolean's value goes after =, as "+c.want) {
			t.Errorf("koan %q: %s", c.args, r.Line())
		}
	}
	// As an extra word, it is usage.
	r := koan(t, home, "show", "/w", "false")
	if r.Kind() != "usage" || strings.Contains(message(r), "after =") {
		t.Errorf("no boolean given: %s", r.Line())
	}
	r = koan(t, home, "version", "false")
	if r.Kind() != "usage" || strings.Contains(message(r), "after =") {
		t.Errorf("no boolean option: %s", r.Line())
	}
}

// batch without -i says -i gives the batch.
func TestBatchWithoutInput(t *testing.T) {
	home := root(t)
	r := fails(t, home, "invalid-input", "", "batch")
	if !strings.Contains(message(r), "-i gives the batch") {
		t.Errorf("message: %s", r.Line())
	}
}

// A message names an option as it is typed, a position by what it holds;
// through --input, the field.
func TestMessageTerms(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/w")
	ok(t, home, "task", "create", "a", "--folder", "/w") // 2
	for _, c := range []struct {
		args    []string
		in      string
		command string
		cli, fl string
	}{
		{[]string{"update", "2", "--extra-merge", "1"}, `{"refs":[2],"extra_merge":1}`, "update", "--extra-merge", "extra_merge"},
		{[]string{"update", "2", "--extra-replace-all", "{}", "--extra-merge", "{}"}, `{"refs":[2],"extra_replace_all":{},"extra_merge":{}}`, "update", "--extra-replace-all", "extra_replace_all"},
		{[]string{"join", "2"}, `{"ref":2}`, "join", "name the folders to join after the task or folder", "join is required"},
		{[]string{"complete"}, `{}`, "complete", "name the tasks", "tasks is required"},
		{[]string{"block", "2"}, `{"task":2}`, "block", "--blocked-by or --blocks", "blocked_by or blocks"},
		{[]string{"task", "list", "/w", "--file", "/x"}, `{"folder":"/w","file":"/x"}`, "task list", "the folder, --file and --here", "folder and file"},
		{[]string{"where"}, `{}`, "where", "the reference, --file and --here", "ref and file"},
		{[]string{"mv", "2"}, `{"refs":[2]}`, "mv", "the destination or --into", "dest or into"},
	} {
		r := koan(t, home, c.args...)
		if r.Kind() != "invalid-input" || !strings.Contains(message(r), c.cli) {
			t.Errorf("koan %q: want %q: %s", c.args, c.cli, r.Line())
		}
		r = input(t, home, c.command, c.in)
		if r.Kind() != "invalid-input" || !strings.Contains(message(r), c.fl) || strings.Contains(message(r), "--") {
			t.Errorf("%s -i %s: want %q: %s", c.command, c.in, c.fl, r.Line())
		}
	}
}

// A message says the facts and the options, and never tells the caller to
// take a step the rules leave to them; it ends with no full stop and
// holds no Go list.
func TestMessagesAreFacts(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/w")
	ok(t, home, "task", "create", "a", "--folder", "/w") // 2
	ok(t, home, "task", "create", "b", "--folder", "/w") // 3
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"archive", "2"}, "@2 is still open; only finished work is archived"},
		{[]string{"archive", "2", "3"}, "@2, @3 are still open; only finished work is archived"},
		{[]string{"archive", "/w"}, "@2, @3 under @1 are still open; only finished work is archived"},
		{[]string{"delete", "/w"}, "@1 holds @2, @3; --contents would delete them too"},
		{[]string{"task", "list", "--state", "x"}, `"x" is not one of open, complete`},
	} {
		r := koan(t, home, c.args...)
		if message(r) != c.want {
			t.Errorf("koan %q: message %q, want %q", c.args, message(r), c.want)
		}
	}
	// Material.
	if err := os.WriteFile(filepath.Join(home, "koan-root", "tree", "@w", "x.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok(t, home, "delete", "2", "3")
	r := fails(t, home, "conflict", "has-material", "delete", "/w")
	if message(r) != "@1 holds material; --material would delete it too" {
		t.Errorf("material: %s", r.Line())
	}
	r = input(t, home, "delete", `{"refs":["/w"]}`)
	if message(r) != "@1 holds material; material would delete it too" {
		t.Errorf("material through --input: %s", r.Line())
	}
	// Not initialized: what makes the root, not an order to run it.
	empty := t.TempDir()
	r = koan(t, empty, "task", "list")
	if r.Kind() != "not-initialized" || !strings.HasSuffix(message(r), "koan-root does not exist; koan init makes it") {
		t.Errorf("not initialized: %s", r.Line())
	}
	// None of the above ends with a full stop or holds a Go list.
	fullStop := regexp.MustCompile(`\.$|\[[a-z]+ [a-z]+`)
	for _, args := range [][]string{{"show", "99"}, {"archive", "/w"}, {"info"}, {"mv", "1"}} {
		r := koan(t, home, args...)
		if m := message(r); fullStop.MatchString(m) {
			t.Errorf("koan %q: message %q", args, m)
		}
	}
}

// A not-found message for an archived item names the path error.archived
// gives as its notes_path, a lone task archived without notes included.
func TestArchivedMessageNamesNotesPath(t *testing.T) {
	home := root(t)
	ok(t, home, "task", "create", "a") // 1
	ok(t, home, "task", "create", "b") // 2
	if err := os.Remove(filepath.Join(home, "koan-root", "tree", "@1.md")); err != nil {
		t.Fatal(err)
	}
	ok(t, home, "complete", "1", "2")
	ok(t, home, "archive", "1")
	ok(t, home, "folder", "create", "/f") // 3
	ok(t, home, "archive", "/f")
	r := fails(t, home, "not-found", "", "show", "1")
	archived := r.Error()["archived"].([]any)[0].(map[string]any)
	path := archived["notes_path"].(string)
	if !strings.HasSuffix(path, "@1.md") || !strings.HasSuffix(message(r), "archived, to "+path) {
		t.Errorf("task: path %s: %s", path, r.Line())
	}
	r = fails(t, home, "not-found", "", "show", "3")
	archived = r.Error()["archived"].([]any)[0].(map[string]any)
	path = archived["notes_path"].(string)
	if !strings.HasSuffix(path, "@notes.md") || !strings.HasSuffix(message(r), "archived, to "+path) {
		t.Errorf("folder: path %s: %s", path, r.Line())
	}
}

// where takes exactly one of its reference, --file and --here, and says
// so; the lists take at most one.
func TestOneOfCounts(t *testing.T) {
	home := root(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"where", "--file", "/tmp", "--here"}, "give exactly one of"},
		{[]string{"task", "list", "--file", "/tmp", "--here"}, "give at most one of"},
	} {
		if m := message(invalidAt(t, home, "", c.args...)); !strings.HasPrefix(m, c.want) {
			t.Errorf("koan %q: %q", c.args, m)
		}
	}
}
