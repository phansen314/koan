package main_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/phansen314/koan/internal/testrun"
)

// standIn is fzf as pick's tests run it (PRD Testing, pick): it records
// its arguments, its lines and FZF_DEFAULT_OPTS in $FAKE_FZF_DIR, copies
// the preview directory named in --preview there, prints the lines whose
// keys $FAKE_FZF_PICK lists, and exits $FAKE_FZF_EXIT. --version prints
// $FAKE_FZF_VERSION, and fails as a bad option in FZF_DEFAULT_OPTS would.
const standIn = `#!/bin/sh
if [ "$1" = --version ]; then
  if [ -n "$FZF_DEFAULT_OPTS" ]; then echo "unknown option: $FZF_DEFAULT_OPTS" >&2; exit 2; fi
  echo "${FAKE_FZF_VERSION:-0.65.0} (stand-in)"
  exit 0
fi
printf '%s\n' "$@" > "$FAKE_FZF_DIR/args"
cat > "$FAKE_FZF_DIR/stdin"
printf '%s' "$FZF_DEFAULT_OPTS" > "$FAKE_FZF_DIR/opts"
while [ $# -gt 0 ]; do
  if [ "$1" = --preview ]; then
    dir=${2#"cat -- '"}
    dir=${dir%%"'/{1}"*}
    cp -R "$dir" "$FAKE_FZF_DIR/previews"
  fi
  shift
done
tab=$(printf '\t')
for key in $FAKE_FZF_PICK; do grep "^$key$tab" "$FAKE_FZF_DIR/stdin"; done
exit "${FAKE_FZF_EXIT:-0}"
`

// picker runs koan pick, with a terminal or without, and the stand-in
// fzf first on PATH.
type picker struct {
	t    *testing.T
	home string
	dir  string // the working directory; home when empty
	tty  bool
	path string   // PATH; the stand-in's directory, then the system's, when empty
	env  []string // more of the environment, such as FAKE_FZF_PICK=@3
	rec  string   // where the stand-in records its run
	xdg  string   // XDG_RUNTIME_DIR, where the previews go
}

func newPicker(t *testing.T, home string) *picker {
	t.Helper()
	fake := t.TempDir()
	if err := os.WriteFile(filepath.Join(fake, "fzf"), []byte(standIn), 0o755); err != nil {
		t.Fatal(err)
	}
	return &picker{t: t, home: home, tty: true, path: fake + string(os.PathListSeparator) + os.Getenv("PATH"),
		rec: t.TempDir(), xdg: t.TempDir()}
}

// picked is one run of koan pick.
type picked struct {
	out, err string
	exit     int
}

func (p *picker) with(env ...string) *picker {
	q := *p
	q.env = append(slices.Clone(p.env), env...)
	return &q
}

func (p *picker) run(args ...string) picked {
	p.t.Helper()
	if err := os.RemoveAll(p.rec); err != nil {
		p.t.Fatal(err)
	}
	if err := os.Mkdir(p.rec, 0o755); err != nil {
		p.t.Fatal(err)
	}
	cmd := exec.Command(testrun.Binary(p.t), append([]string{"pick"}, args...)...)
	cmd.Dir = p.dir
	if cmd.Dir == "" {
		cmd.Dir = p.home
	}
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "HOME", "PWD", "PATH", "XDG_RUNTIME_DIR", "FZF_DEFAULT_OPTS", "FZF_DEFAULT_OPTS_FILE":
		default:
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "HOME="+p.home, "PWD="+cmd.Dir, "PATH="+p.path, "XDG_RUNTIME_DIR="+p.xdg, "FAKE_FZF_DIR="+p.rec)
	cmd.Env = append(cmd.Env, p.env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// A new session has no controlling terminal, so /dev/tty doesn't open,
	// unless the pseudo-terminal becomes it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if p.tty {
		master, tty, err := openPTY()
		if err != nil {
			p.t.Fatalf("opening a pseudo-terminal: %v", err)
		}
		defer master.Close()
		defer tty.Close()
		cmd.ExtraFiles = []*os.File{tty}
		cmd.SysProcAttr.Setctty, cmd.SysProcAttr.Ctty = true, 3
	}
	err := cmd.Run()
	res := picked{out: stdout.String(), err: stderr.String()}
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		res.exit = exit.ExitCode()
	case err != nil:
		p.t.Fatalf("koan pick %q: %v", args, err)
	}
	if strings.Count(res.err, "\n") > 1 {
		p.t.Errorf("koan pick %q: more than one line on stderr:\n%s", args, res.err)
	}
	return res
}

// record is a file the stand-in wrote, or "" when it didn't run.
func (p *picker) record(name string) string {
	b, err := os.ReadFile(filepath.Join(p.rec, name))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		p.t.Fatal(err)
	}
	return string(b)
}

// keys are the keys of the lines pick gave fzf, in order.
func (p *picker) keys() []string {
	var keys []string
	for _, l := range strings.Split(strings.TrimSuffix(p.record("stdin"), "\n"), "\n") {
		if l != "" {
			key, _, _ := strings.Cut(l, "\t")
			keys = append(keys, key)
		}
	}
	return keys
}

// line is the line pick gave fzf for key: its notes path and its columns.
func (p *picker) line(key string) (notes string, cols []string) {
	p.t.Helper()
	for _, l := range strings.Split(p.record("stdin"), "\n") {
		if k, rest, _ := strings.Cut(l, "\t"); k == key {
			notes, shown, _ := strings.Cut(rest, "\t")
			return notes, regexp.MustCompile(` {2,}`).Split(shown, -1)
		}
	}
	p.t.Fatalf("no line for %s in:\n%s", key, p.record("stdin"))
	return "", nil
}

// failed checks a run failed with kind, printing nothing on stdout and
// the one standard-error line.
func (r picked) failed(t *testing.T, exit int, kind, message string) {
	t.Helper()
	if r.exit != exit || r.out != "" || !strings.HasPrefix(r.err, "koan: "+kind+": ") || !strings.Contains(r.err, message) {
		t.Errorf("want exit %d, %s %q; got exit %d, stdout %q, stderr %q", exit, kind, message, r.exit, r.out, r.err)
	}
}

// pickTree is the tree pick's tests pick from:
//
//	/          Root task @7
//	/work      @1 "Work": Announce @5 (after /work/api), Old @6 (complete)
//	/work/api  @2: Design the endpoints @3 (p2, #api), Build them @4
//	           (claimed), its start gate @8 and done gate @9
func pickTree(t *testing.T) string {
	t.Helper()
	home := root(t)
	r := batchOK(t, home, `{"folders": [{"path": "/work", "title": "Work"}, {"path": "/work/api"}],
	  "tasks": [
	    {"label": "$design", "title": "Design the endpoints", "folder": "/work/api", "priority": 2, "tags": ["api"],
	     "blocked_by": ["/work/api.start"], "blocks": ["/work/api"]},
	    {"label": "$build", "title": "Build them", "folder": "/work/api", "blocked_by": ["$design"], "blocks": ["/work/api"]},
	    {"title": "Announce", "folder": "/work", "blocked_by": ["/work/api"]},
	    {"title": "Old", "folder": "/work"},
	    {"title": "Root task"}]}`)
	res := r.Result()
	got, _ := json.Marshal([]any{res["folders"], res["tasks"], res["gates_made"]})
	want := `[[{"id":1,"path":"/work"},{"id":2,"path":"/work/api"}],[3,4,5,6,7],` +
		`[{"end":"start","folder":{"id":2,"path":"/work/api"},"id":8},{"end":"done","folder":{"id":2,"path":"/work/api"},"id":9}]]`
	if string(got) != want {
		t.Fatalf("the tree's IDs: %s", got)
	}
	ok(t, home, "complete", "6")
	ok(t, home, "claim", "4")
	return home
}

// pick offers every folder, gate and open task in tree order: a folder,
// its gates not made yet, its own tasks with its made gates among them,
// then its folders; the root's tasks first, with no line of its own.
func TestPickCandidates(t *testing.T) {
	home := pickTree(t)
	p := newPicker(t, home)
	r := p.with("FAKE_FZF_PICK=@3").run()
	if r.exit != 0 || r.out != "@3\n" || r.err != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q", r.exit, r.out, r.err)
	}
	want := []string{"@7", "@1", "@1.start", "@1.done", "@5", "@2", "@3", "@4", "@2.start", "@2.done"}
	if got := p.keys(); !slices.Equal(got, want) {
		t.Errorf("keys %v, want %v", got, want)
	}
	tree := filepath.Join(home, "koan-root", "tree")
	for key, want := range map[string]struct {
		notes string
		cols  []string
	}{
		"@7":       {filepath.Join(tree, "@7.md"), []string{"task", "@7", "/", "Root task", "ready"}},
		"@1":       {filepath.Join(tree, "@work", "@notes.md"), []string{"folder", "@1", "/work", "Work", "5 open"}},
		"@1.start": {"", []string{"gate", "@1.start", "/work", "Start", "not made"}},
		"@5":       {filepath.Join(tree, "@work", "@5.md"), []string{"task", "@5", "/work", "Announce", "blocked"}},
		"@2":       {filepath.Join(tree, "@work", "@api", "@notes.md"), []string{"folder", "@2", "/work/api", "api", "4 open"}},
		"@3":       {filepath.Join(tree, "@work", "@api", "@3.md"), []string{"task", "@3", "/work/api", "Design the endpoints", "blocked", "p2 #api"}},
		"@4":       {filepath.Join(tree, "@work", "@api", "@4.md"), []string{"task", "@4", "/work/api", "Build them", "blocked", "claimed"}},
		"@2.start": {filepath.Join(tree, "@work", "@api", "@start.md"), []string{"gate", "@2.start", "/work/api", "Start", "ready"}},
		"@2.done":  {filepath.Join(tree, "@work", "@api", "@done.md"), []string{"gate", "@2.done", "/work/api", "Done", "blocked"}},
	} {
		notes, cols := p.line(key)
		if notes != want.notes || !slices.Equal(cols, want.cols) {
			t.Errorf("%s: notes %q, columns %q; want %q, %q", key, notes, cols, want.notes, want.cols)
		}
	}
	// Every column but the last starts at the same place on every line.
	var starts []int
	for i, l := range strings.Split(strings.TrimSuffix(p.record("stdin"), "\n"), "\n") {
		_, rest, _ := strings.Cut(l, "\t")
		_, shown, _ := strings.Cut(rest, "\t")
		var at []int
		for _, m := range regexp.MustCompile(` {2,}`).FindAllStringIndex(shown, 4) {
			at = append(at, m[1])
		}
		if i == 0 {
			starts = at
		} else if !slices.Equal(at[:min(len(at), 3)], starts[:3]) {
			t.Errorf("line %d's columns start at %v, the first's at %v:\n%s", i, at, starts, p.record("stdin"))
		}
	}
}

// The filters are task list's, with the frontier; a task filter keeps
// only the folders holding a match and hides the gates not made yet,
// while --state doesn't.
func TestPickFilters(t *testing.T) {
	home := pickTree(t)
	p := newPicker(t, home).with("FAKE_FZF_EXIT=1")
	for _, c := range []struct {
		args []string
		want []string
	}{
		{[]string{"--kinds", "folder"}, []string{"@1", "@2"}},
		{[]string{"--kinds", "gate"}, []string{"@1.start", "@1.done", "@2.start", "@2.done"}},
		{[]string{"--kinds", "task,folder", "--tags-any", "api"}, []string{"@1", "@2", "@3"}},
		{[]string{"--kinds", "folder", "--tags-all", "api"}, []string{"@1", "@2"}},
		{[]string{"--readiness", "ready"}, []string{"@7", "@1", "@2", "@2.start"}},
		{[]string{"--claimed"}, []string{"@1", "@2", "@4"}},
		{[]string{"--claimed=false", "/work/api"}, []string{"@2", "@3", "@2.start", "@2.done"}},
		{[]string{"--frontier"}, []string{"@7", "@2.start", "@1", "@2"}},
		{[]string{"--frontier", "/work"}, []string{"@2.start", "@1", "@2"}},
		{[]string{"--state", "complete"}, []string{"@1", "@1.start", "@1.done", "@6", "@2"}},
		{[]string{"--state", "open,complete", "--kinds", "task"}, []string{"@7", "@5", "@6", "@3", "@4"}},
		{[]string{"/work/api"}, []string{"@2", "@3", "@4", "@2.start", "@2.done"}},
		{[]string{"@1", "--recursive=false"}, []string{"@1", "@1.start", "@1.done", "@5", "@2"}},
		// The match is deeper than --recursive=false looks: nothing to offer.
		{[]string{"/work", "--recursive=false", "--tags-any", "api"}, nil},
	} {
		r := p.run(c.args...)
		if r.exit != 1 || r.out != "" || r.err != "" {
			t.Errorf("%q: exit %d, stdout %q, stderr %q", c.args, r.exit, r.out, r.err)
		}
		if got := p.keys(); !slices.Equal(got, c.want) {
			t.Errorf("%q: keys %v, want %v", c.args, got, c.want)
		}
	}
}

// pick takes the folder it picks from as its subject, one word, and
// never with --here; --folder is gone.
func TestPickSubject(t *testing.T) {
	home := root(t)
	batchOK(t, home, `{"folders": [{"path": "/work"}, {"path": "/work/api"}],
	  "tasks": [{"title": "a", "folder": "/work/api"}, {"title": "b"}]}`)
	p := newPicker(t, home).with("FAKE_FZF_EXIT=1")
	if r := p.run("/work/api"); r.exit != 1 || r.out != "" || r.err != "" {
		t.Errorf("/work/api: exit %d, stdout %q, stderr %q", r.exit, r.out, r.err)
	}
	if got, want := p.keys(), []string{"@2", "@2.start", "@2.done", "@3"}; !slices.Equal(got, want) {
		t.Errorf("/work/api: keys %v, want %v", got, want)
	}
	p.run("--here", "/work").failed(t, 1, "invalid-input", "folder and file")
	p.run("/work", "/work/api").failed(t, 2, "usage", "")
	p.run("--folder", "/work").failed(t, 2, "usage", "folder")
}

// pick prints what was picked in the candidates' order, comma-joined;
// gives fzf the spec's options; and removes its previews.
func TestPickOutcome(t *testing.T) {
	home := pickTree(t)
	p := newPicker(t, home)
	r := p.with("FAKE_FZF_PICK=@2.done @4 @1", "FZF_DEFAULT_OPTS=--bogus").run("--query", "a b")
	if r.exit != 0 || r.out != "@1,@4,@2.done\n" || r.err != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q", r.exit, r.out, r.err)
	}
	if got := p.record("opts"); got != "--bogus" {
		t.Errorf("FZF_DEFAULT_OPTS reached fzf as %q", got)
	}
	args := strings.Split(strings.TrimSuffix(p.record("args"), "\n"), "\n")
	i := slices.Index(args, "--preview")
	if i < 0 {
		t.Fatalf("no --preview in %q", args)
	}
	dir := strings.TrimSuffix(strings.TrimPrefix(args[i+1], "cat -- '"), "'/{1} {2} 2>/dev/null")
	if filepath.Dir(dir) != p.xdg || !strings.HasPrefix(filepath.Base(dir), "koan-pick-") {
		t.Errorf("previews in %s, not koan-pick-<random> in %s", dir, p.xdg)
	}
	want := []string{
		"--no-select-1", "--no-exit-0", "--no-expect", "--no-tmux", "--no-read0", "--no-header-lines",
		"--no-print0", "--no-print-query", "--no-tac", "--accept-nth", "..",
		"--multi", "--delimiter", "\t", "--with-nth", "3..", "--tiebreak", "index", "--with-shell", "sh -c",
		"--preview", "cat -- '" + dir + "'/{1} {2} 2>/dev/null", "--preview-window", "down,50%", "--query", "a b",
	}
	// The tab is a line of its own in the record: split, it is "\t".
	if !slices.Equal(args, want) {
		t.Errorf("fzf's arguments:\n%q\nwant\n%q", args, want)
	}
	if left, _ := os.ReadDir(p.xdg); len(left) != 0 {
		t.Errorf("the previews were left behind: %v", left)
	}
	info, err := os.Stat(filepath.Join(p.rec, "previews"))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("the preview directory: %v, %v", info, err)
	}

	previews := map[string]string{
		"@3": "@3 (/work/api)\n" +
			"title:       Design the endpoints\n" +
			"folder:      /work/api\n" +
			"state:       open\n" +
			"readiness:   blocked\n" +
			"priority:    2\n" +
			"tags:        api\n" +
			"waits on:    @8 (/work/api.start) open\n" +
			"blocks:      @4 (/work/api) open\n" +
			"             @9 (/work/api.done) open\n" +
			"claim:       —\n\n",
		"@1": "@1 (/work)\n" +
			"title:       Work\n" +
			"open:        5\n" +
			"complete:    1\n" +
			"finished:    false\n" +
			"start gate:  not made\n" +
			"done gate:   not made\n\n",
		"@1.done": "@1.done (/work.done)\n" +
			"state:       not made yet: the first edge naming it makes it\n\n",
	}
	for key, want := range previews {
		b, err := os.ReadFile(filepath.Join(p.rec, "previews", key))
		if err != nil || string(b) != want {
			t.Errorf("%s's preview: %v\n%s\nwant\n%s", key, err, b, want)
		}
	}
	b, _ := os.ReadFile(filepath.Join(p.rec, "previews", "@4"))
	if !regexp.MustCompile(`(?m)^claim:       since \d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ \{\}$`).Match(b) {
		t.Errorf("@4's claim:\n%s", b)
	}
}

// Cancelling exits 130 and nothing matching exits 1, both printing
// nothing; any other fzf exit is unavailable.
func TestPickFzfExits(t *testing.T) {
	home := pickTree(t)
	p := newPicker(t, home)
	for _, c := range []struct {
		env  string
		exit int
	}{{"FAKE_FZF_EXIT=130", 130}, {"FAKE_FZF_EXIT=1", 1}} {
		if r := p.with(c.env).run(); r.exit != c.exit || r.out != "" || r.err != "" {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", c.env, r.exit, r.out, r.err)
		}
	}
	p.with("FAKE_FZF_EXIT=2").run().failed(t, 1, "unavailable", "fzf exited with status 2")
	if left, _ := os.ReadDir(p.xdg); len(left) != 0 {
		t.Errorf("the previews were left behind: %v", left)
	}
}

// With nothing to offer, fzf never opens.
func TestPickNoCandidates(t *testing.T) {
	p := newPicker(t, root(t))
	if r := p.run(); r.exit != 1 || r.out != "" || r.err != "" {
		t.Errorf("exit %d, stdout %q, stderr %q", r.exit, r.out, r.err)
	}
	if p.record("args") != "" {
		t.Error("fzf ran")
	}
}

// Failures print only the standard-error line, in the spec's order of
// checks: the input, the root, the terminal, fzf, the references.
func TestPickFailures(t *testing.T) {
	home := pickTree(t)
	p := newPicker(t, home)
	p.run("--frob").failed(t, 2, "usage", "frob")
	p.run("--frontier", "--readiness", "ready").failed(t, 1, "invalid-input", "frontier")
	p.run("--here", "/work").failed(t, 1, "invalid-input", "folder and file")
	p.run("--kinds", "material").failed(t, 1, "invalid-input", "material")
	p.run("/nope").failed(t, 1, "not-found", "/nope")

	fresh := newPicker(t, t.TempDir())
	fresh.run().failed(t, 1, "not-initialized", "")

	noTTY := *p
	noTTY.tty = false
	noTTY.run("/nope").failed(t, 1, "unavailable", "no terminal")

	noFzf := *p
	noFzf.path = t.TempDir()
	noFzf.run().failed(t, 1, "unavailable", "fzf not found on PATH: pick needs fzf 0.63.0 or later")
	p.with("FAKE_FZF_VERSION=0.62.9").run().failed(t, 1, "unavailable", "fzf 0.62.9 is too old")
	p.with("FAKE_FZF_VERSION=nonsense").run().failed(t, 1, "unavailable", "fzf nonsense is too old")
	if r := p.with("FAKE_FZF_VERSION=0.63.0-dev", "FAKE_FZF_EXIT=1").run(); r.exit != 1 || r.err != "" {
		t.Errorf("fzf 0.63.0-dev: exit %d, stderr %q", r.exit, r.err)
	}
}

// --here picks from the folder the working directory's binds, and
// fails as context does without one; -i gives the whole input.
func TestPickHereAndInput(t *testing.T) {
	home := pickTree(t)
	work := t.TempDir()
	ok(t, home, "bind", "@2", work)
	p := newPicker(t, home).with("FAKE_FZF_EXIT=1")
	p.dir = filepath.Join(work)
	p.run("--here", "--kinds", "folder,task")
	if got, want := p.keys(), []string{"@2", "@3", "@4"}; !slices.Equal(got, want) {
		t.Errorf("--here: keys %v, want %v", got, want)
	}
	p.dir = t.TempDir()
	p.run("--here").failed(t, 1, "not-found", "no marker")

	in := filepath.Join(t.TempDir(), "in.json")
	os.WriteFile(in, []byte(`{"kinds": ["folder"], "file": "`+work+`"}`), 0o644)
	p.run("-i", in)
	if got, want := p.keys(), []string{"@2"}; !slices.Equal(got, want) {
		t.Errorf("-i: keys %v, want %v", got, want)
	}
}

// binds is the lines of a keybinding script, other than its comments,
// that bind a key; there should be none (spec pick, --bash and --zsh).
func binds(script string) []string {
	var found []string
	for _, l := range strings.Split(script, "\n") {
		if l = strings.TrimSpace(l); !strings.HasPrefix(l, "#") && (strings.HasPrefix(l, "bind ") || strings.HasPrefix(l, "bindkey ")) {
			found = append(found, l)
		}
	}
	return found
}

// --bash prints the widget's script without a root or a terminal, and
// binds no key; on a bash older than 4.0 it defines nothing.
func TestPickBash(t *testing.T) {
	p := newPicker(t, t.TempDir())
	p.tty = false
	r := p.run("--bash")
	if r.exit != 0 || r.err != "" || !strings.Contains(r.out, "__koan_pick_widget() {") {
		t.Fatalf("exit %d, stderr %q:\n%s", r.exit, r.err, r.out)
	}
	if b := binds(r.out); len(b) > 0 {
		t.Errorf("binds a key: %q", b)
	}
	if bash, err := exec.LookPath("bash"); err == nil {
		check := exec.Command(bash, "-n")
		check.Stdin = strings.NewReader(r.out)
		if out, err := check.CombinedOutput(); err != nil {
			t.Errorf("bash -n: %v\n%s", err, out)
		}
		out, err := exec.Command(bash, "-c", r.out+"\necho ${BASH_VERSINFO[0]} $(type -t __koan_pick_widget)").Output()
		major, kind, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
		if v, _ := strconv.Atoi(major); err != nil || (v >= 4) != (kind == "function") {
			t.Errorf("bash %s defines %q: %v", major, kind, err)
		}
	}
	p.run("--bash", "--query", "x").failed(t, 1, "invalid-input", "bash takes no other field")
}

// --zsh prints the widget's script without a root or a terminal, and
// loading it makes the widget and binds no key.
func TestPickZsh(t *testing.T) {
	p := newPicker(t, t.TempDir())
	p.tty = false
	r := p.run("--zsh")
	if r.exit != 0 || r.err != "" || !strings.Contains(r.out, "zle -N __koan_pick_widget") {
		t.Fatalf("exit %d, stderr %q:\n%s", r.exit, r.err, r.out)
	}
	if b := binds(r.out); len(b) > 0 {
		t.Errorf("binds a key: %q", b)
	}
	if zsh, err := exec.LookPath("zsh"); err == nil {
		check := exec.Command(zsh, "-n")
		check.Stdin = strings.NewReader(r.out)
		if out, err := check.CombinedOutput(); err != nil {
			t.Errorf("zsh -n: %v\n%s", err, out)
		}
		load := exec.Command(zsh, "-f", "-c", `before=$(bindkey -L)
eval "$KOAN_SCRIPT"
[[ $before == "$(bindkey -L)" ]] || echo "bound a key"
zle -l | grep -qx __koan_pick_widget || echo "no widget"`)
		load.Env = append(os.Environ(), "KOAN_SCRIPT="+r.out)
		if out, err := load.CombinedOutput(); err != nil || len(out) > 0 {
			t.Errorf("loading the script: %v\n%s", err, out)
		}
	}
	p.run("--zsh", "--query", "x").failed(t, 1, "invalid-input", "zsh takes no other field")
	p.run("--zsh", "--bash").failed(t, 1, "invalid-input", "takes no other field")
}

// A real fzf, in a pseudo-terminal: Enter picks the line under the
// cursor, and Esc cancels.
func TestPickRealFzf(t *testing.T) {
	if testing.Short() {
		t.Skip("drives a real fzf")
	}
	fzf, err := exec.LookPath("fzf")
	if err != nil {
		t.Skip("no fzf on PATH")
	}
	home := pickTree(t)
	for _, c := range []struct {
		key  string
		exit int
		out  string
	}{{"\r", 0, "@5\n"}, {"\x1b", 130, ""}} {
		master, tty, err := openPTY()
		if err != nil {
			t.Fatal(err)
		}
		unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 24, Col: 100})
		cmd := exec.Command(testrun.Binary(t), "pick", "--query", "Announce")
		cmd.Dir = home
		cmd.Env = []string{"HOME=" + home, "PATH=" + filepath.Dir(fzf) + ":/usr/bin:/bin", "TERM=xterm-256color", "XDG_RUNTIME_DIR=" + t.TempDir()}
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		cmd.ExtraFiles = []*os.File{tty}
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 3}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		tty.Close()
		var mu sync.Mutex
		var screen bytes.Buffer
		go func() {
			buf := make([]byte, 4096)
			for {
				n, err := master.Read(buf)
				mu.Lock()
				screen.Write(buf[:n])
				mu.Unlock()
				if err != nil {
					return
				}
			}
		}()
		deadline := time.Now().Add(10 * time.Second)
		for {
			mu.Lock()
			// The match, and its preview's header.
			drawn := bytes.Contains(screen.Bytes(), []byte("1/10")) && bytes.Contains(screen.Bytes(), []byte("@5 (/work)"))
			mu.Unlock()
			if drawn {
				break
			}
			if time.Now().After(deadline) {
				cmd.Process.Kill()
				t.Fatalf("fzf never drew the match:\n%q", screen.String())
			}
			time.Sleep(20 * time.Millisecond)
		}
		master.Write([]byte(c.key))
		err = cmd.Wait()
		master.Close()
		exit := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exit = ee.ExitCode()
		}
		if exit != c.exit || stdout.String() != c.out || stderr.String() != "" {
			t.Errorf("%q: exit %d, stdout %q, stderr %q", c.key, exit, stdout.String(), stderr.String())
		}
	}
}
