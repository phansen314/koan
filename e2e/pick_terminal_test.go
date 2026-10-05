package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The picker end to end, in a terminal, against each fzf under test
// (pick-spec.md, Testing). Each test drives pick's fzf through the
// terminal's keys and reads pick's envelope.

// pickOut is pick's envelope, in the parts these tests check.
type pickOut struct {
	OK     bool `json:"ok"`
	Result struct {
		Tasks []struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
		} `json:"tasks"`
		Missing     []int64 `json:"missing"`
		Actions     []any   `json:"actions"`
		NotesEdited []int64 `json:"notes_edited"`
	} `json:"result"`
	Error *struct {
		Kind    string `json:"kind"`
		Message string `json:"message"`
		Details struct {
			Actions []any `json:"actions"`
			Error   *struct {
				Kind string `json:"kind"`
			} `json:"error"`
		} `json:"details"`
	} `json:"error"`
}

// decode checks r is one envelope and decodes it.
func decode(t *testing.T, r result) pickOut {
	t.Helper()
	envelope(t, r)
	var out pickOut
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// picked checks r is a successful pick with no actions, and returns the
// selected IDs, in order.
func picked(t *testing.T, r result) []int64 {
	t.Helper()
	out := decode(t, r)
	res := out.Result
	if r.code != 0 || !out.OK || res.Actions == nil || len(res.Actions) != 0 || res.Missing == nil || res.NotesEdited == nil {
		t.Fatalf("exit %d: %s", r.code, r.stdout)
	}
	ids := []int64{}
	for _, task := range res.Tasks {
		ids = append(ids, task.ID)
	}
	return ids
}

// pickTree is a tree with tasks 1 to 3 in /trips, for the picker. In the
// picker's line order they are 1, 2, 3, so 1 is the first line, at the
// bottom in fzf's default layout, and up moves to the next.
func pickTree(t *testing.T) *tree {
	t.Helper()
	tr := newTree(t)
	for _, c := range [][]string{
		{"create-folder", "/trips"},
		{"create", "Book flights", "--folder", "/trips"},
		{"create", "Renew passport", "--folder", "/trips"},
		{"create", "Pack bags", "--folder", "/trips"},
	} {
		if r := run(t, tr.cmd(c...)); r.code != 0 {
			t.Fatal(r.stdout)
		}
	}
	return tr
}

// setQuery types q and waits for fzf to match it, to n lines, and put the
// cursor on the first of them.
func (p *picker) setQuery(q string, n int) fzfState {
	p.t.Helper()
	p.send(q)
	return p.waitState(fmt.Sprintf("query %q matching %d", q, n), func(st fzfState) bool {
		return st.Query == q && st.MatchCount == n && (n == 0) == (st.Current == nil) && (n == 0 || st.Current.Index == st.Matches[0].Index)
	})
}

// command enters command mode with Esc, and waits for its header.
func (p *picker) command() {
	p.t.Helper()
	p.send(keyEsc)
	p.waitScreen("[cmd]")
}

// quit quits from insert mode: Esc, then Esc again in command mode.
func (p *picker) quit() {
	p.t.Helper()
	p.command()
	p.send(keyEsc)
}

// Enter, quit and cancel, and the edges of each: Enter or quit with no line
// to pick gives an empty selection, not an error. FZF_DEFAULT_OPTS that
// would end fzf without a callback changes none of it.
func TestPickOutcomes(t *testing.T) {
	type outcome struct {
		name  string
		empty bool // on a tree with no tasks
		keys  func(p *picker)
		want  []int64 // nil: cancelled
	}
	outcomes := []outcome{
		{"enter", false, func(p *picker) { p.send(keyEnter) }, []int64{1}},
		{"enter after moving", false, func(p *picker) {
			p.send(keyUp)
			p.waitState("the cursor on 2", func(st fzfState) bool { return lineKey(st.Current) == "2@/trips" })
			p.send(keyEnter)
		}, []int64{2}},
		{"enter on the query's match", false, func(p *picker) {
			p.setQuery("pass", 1)
			p.send(keyEnter)
		}, []int64{2}},
		// Marked 3 then 1, emitted in line order.
		{"enter with marks", false, func(p *picker) {
			p.send(keyUp + keyUp)
			p.waitState("the cursor on 3", func(st fzfState) bool { return lineKey(st.Current) == "3@/trips" })
			p.send(keyTab)
			p.waitState("3 marked", func(st fzfState) bool { return len(st.Selected) == 1 })
			p.send(keyDown + keyDown)
			p.waitState("the cursor on 1", func(st fzfState) bool { return lineKey(st.Current) == "1@/trips" })
			p.send(keyTab)
			p.waitState("1 marked", func(st fzfState) bool { return len(st.Selected) == 2 })
			p.send(keyEnter)
		}, []int64{1, 3}},
		{"enter, query matching nothing", false, func(p *picker) {
			p.setQuery("zzz", 0)
			p.send(keyEnter)
		}, []int64{}},
		{"enter on an empty list", true, func(p *picker) { p.send(keyEnter) }, []int64{}},
		{"quit", false, func(p *picker) { p.quit() }, []int64{}},
		{"quit with q", false, func(p *picker) {
			p.command()
			p.send("q")
		}, []int64{}},
		{"enter in command mode", false, func(p *picker) {
			p.command()
			p.send(keyEnter)
		}, []int64{1}},
		{"quit with marks", false, func(p *picker) {
			p.send(keyTab)
			p.waitState("1 marked", func(st fzfState) bool { return len(st.Selected) == 1 })
			p.quit()
		}, []int64{}},
		{"quit, query matching nothing", false, func(p *picker) {
			p.setQuery("zzz", 0)
			p.quit()
		}, []int64{}},
		{"quit on an empty list", true, func(p *picker) { p.quit() }, []int64{}},
		{"cancel", false, func(p *picker) { p.send(keyCtrlC) }, nil},
		{"cancel on an empty list", true, func(p *picker) { p.send(keyCtrlC) }, nil},
	}
	eachFzf(t, func(t *testing.T, fzfDir string) {
		full, empty := pickTree(t), newTree(t)
		one := newTree(t)
		if r := run(t, one.cmd("create", "Only")); r.code != 0 {
			t.Fatal(r.stdout)
		}
		for _, opts := range []string{"", "--select-1 --exit-0 --expect=esc"} {
			// One candidate: --select-1 would take it, without a
			// callback, as soon as the list is in.
			for _, key := range []string{"enter", "quit"} {
				name := key + " on the only task"
				if opts != "" {
					name += ", FZF_DEFAULT_OPTS"
				}
				t.Run(name, func(t *testing.T) {
					cmd := one.cmd("pick", "--fields", "id")
					cmd.Env = append(cmd.Env, "FZF_DEFAULT_OPTS="+opts)
					p := startPick(t, fzfDir, cmd)
					if p.done() {
						t.Fatalf("pick exited before the picker opened: %s", p.stdout.String())
					}
					if st := p.loaded(); st.TotalCount != 1 {
						t.Fatalf("state %+v", st)
					}
					want := []int64{1}
					if key == "quit" {
						p.quit()
						want = []int64{}
					} else {
						p.send(keyEnter)
					}
					if got := picked(t, p.result()); !slices.Equal(got, want) {
						t.Errorf("picked %v, want %v", got, want)
					}
				})
			}
			for _, o := range outcomes {
				name := o.name
				if opts != "" {
					name += ", FZF_DEFAULT_OPTS"
				}
				t.Run(name, func(t *testing.T) {
					tr := full
					if o.empty {
						tr = empty
					}
					cmd := tr.cmd("pick", "--fields", "id")
					if opts != "" {
						cmd.Env = append(cmd.Env, "FZF_DEFAULT_OPTS="+opts)
					}
					p := startPick(t, fzfDir, cmd)
					if p.done() {
						t.Fatalf("pick exited before the picker opened: %s", p.stdout.String())
					}
					st := p.loaded()
					if o.empty != (st.TotalCount == 0) {
						t.Fatalf("state %+v", st)
					}
					o.keys(p)
					r := p.result()
					if o.want == nil {
						out := decode(t, r)
						if r.code != 1 || out.Error == nil || out.Error.Kind != "cancelled" || out.Error.Details.Actions == nil || len(out.Error.Details.Actions) != 0 {
							t.Errorf("exit %d: %s", r.code, r.stdout)
						}
						return
					}
					if got := picked(t, r); !slices.Equal(got, o.want) {
						t.Errorf("picked %v, want %v", got, o.want)
					}
				})
			}
		}
	})
}

// Text from data is shown literally and never run: a title, notes with a
// newline, and the initial query, in the input line and command mode's
// header, each holding fzf action syntax
// (pick-spec.md, fzf contract: No data in action text).
func TestPickHostileText(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		// X is relative: callbacks run where pick does. Not the spec's
		// )+execute-silent(touch X)+(, whose last ( leaves a chain fzf
		// would refuse whole, but one fzf would run if it were injected.
		dir := t.TempDir()
		hostile := ")+execute-silent(touch X)+change-footer("
		tr := newTree(t)
		if r := run(t, tr.cmd("create", hostile, "--notes", "first\n"+hostile+"\nlast")); r.code != 0 {
			t.Fatal(r.stdout)
		}
		if r := run(t, tr.cmd("create", "Plain")); r.code != 0 {
			t.Fatal(r.stdout)
		}
		cmd := tr.cmd("pick", "--fields", "id,title", "--query", hostile)
		cmd.Dir = dir
		p := startPick(t, fzfDir, cmd)
		st := p.loaded()
		if st.Query != hostile || lineKey(st.Current) != "1@/" {
			t.Fatalf("state %+v", st)
		}
		// The line, the preview's title, and the notes' middle line.
		p.waitFor("the title in the list and the preview, and the notes", func() bool {
			s := p.screen()
			return strings.Count(s, hostile) >= 3 && strings.Contains(s, "last")
		})
		// Command mode's header shows the hidden query.
		p.command()
		// As much of it as fits the list beside the preview.
		p.waitScreen("[cmd] query: " + hostile[:27])
		p.send("i")
		p.waitScreen("open> " + hostile)
		p.post("change-query()")
		p.waitState("both lines", func(st fzfState) bool { return st.MatchCount == 2 })
		p.send(keyEnter)
		r := p.result()
		if got := picked(t, r); !slices.Equal(got, []int64{1}) {
			t.Errorf("picked %v", got)
		}
		if _, err := os.Stat(filepath.Join(dir, "X")); !os.IsNotExist(err) {
			t.Errorf("hostile text ran: %v", err)
		}
	})
}

// The modes (pick-spec.md, Modes): in insert mode every key types; command
// mode hides the query, keeps it filtering, ignores keys it doesn't bind,
// even those that would edit the query, and has its own keys.
func TestPickModes(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		p := startPick(t, fzfDir, tr.cmd("pick", "--fields", "id"))
		p.loaded()
		cursorOn := func(i int) {
			t.Helper()
			p.waitState(fmt.Sprintf("the cursor on line %d", i), func(st fzfState) bool { return st.Current != nil && st.Current.Index == i })
		}

		// Insert mode: command mode's keys type.
		p.setQuery("jkgGq ?/i", 0)
		p.send(keyCtrlU)
		p.setQuery("s", 3)

		p.command()
		p.waitScreen("[cmd] query: s")
		if strings.Contains(p.screen(), "open> ") {
			t.Errorf("input line shown in command mode:\n%s", p.screen())
		}
		// Keys command mode doesn't bind, and those bound to edit the
		// query, do nothing while it is hidden; k after them shows they
		// were handled.
		p.send("zw\x7f" + keyCtrlU + keyCtrlD + "\x17" + "k")
		cursorOn(1)
		if st := p.state(); st.Query != "s" || st.MatchCount != 3 {
			t.Errorf("query %q matching %d", st.Query, st.MatchCount)
		}
		p.send("G")
		cursorOn(2)
		p.send("g")
		cursorOn(0)
		p.send("k")
		cursorOn(1)
		p.send("j")
		cursorOn(0)

		// Marks: space, and Tab.
		p.send(" ")
		p.waitState("a mark", func(st fzfState) bool { return len(st.Selected) == 1 })
		p.send("k")
		cursorOn(1)
		p.send(keyTab)
		p.waitState("two marks", func(st fzfState) bool { return len(st.Selected) == 2 })

		// ? shows the keys in the preview until the cursor moves. Tab
		// moved down after marking, to line 0, so k goes to 2's line.
		p.send("?")
		p.waitScreen("until the cursor moves")
		p.send("k")
		p.waitFor("the help gone", func() bool {
			s := p.screen()
			return !strings.Contains(s, "until the cursor moves") && strings.Contains(s, "#2 Renew passport")
		})

		// i back to insert mode: the query shows, and keys edit and type.
		p.send("i")
		p.waitFor("insert mode", func() bool {
			s := p.screen()
			return strings.Contains(s, "open> s") && !strings.Contains(s, "[cmd]")
		})
		p.send(keyCtrlU)
		p.setQuery("j", 0)
		// ctrl-space to command mode, / back.
		p.send("\x00")
		p.waitScreen("[cmd] query: j")
		p.send("/")
		p.waitScreen("open> j")
		p.send(keyCtrlU)
		p.waitState("the query cleared", func(st fzfState) bool { return st.Query == "" && st.MatchCount == 3 })

		p.send(keyEnter)
		if got := picked(t, p.result()); !slices.Equal(got, []int64{1, 2}) {
			t.Errorf("picked %v", got)
		}
	})
}

// c, the first action, end to end: marked lines completed in line order,
// the list reloaded without them and with the marks cleared, the status
// line, command mode kept, and every call in the output's actions; then,
// with every target shown complete, c reopens.
func TestPickComplete(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		p := startPick(t, fzfDir, tr.cmd("pick", "--fields", "id"))
		p.loaded()
		p.command()
		// Mark 3, then 1: space marks and moves down.
		p.send("G ")
		p.waitState("3 marked", func(st fzfState) bool { return len(st.Selected) == 1 })
		p.send("j ")
		p.waitState("1 marked", func(st fzfState) bool { return len(st.Selected) == 2 })
		p.send("c")
		p.waitScreen("✓ completed 2: 1, 3")
		st := p.waitState("the reload", func(st fzfState) bool { return st.TotalCount == 1 })
		if len(st.Selected) != 0 || lineKey(st.Current) != "2@/trips" {
			t.Errorf("after c: %+v", st)
		}
		if !strings.Contains(p.screen(), "[cmd]") {
			t.Errorf("left command mode:\n%s", p.screen())
		}
		p.send(keyEnter)
		r := p.result()
		out := decode(t, r)
		var ops []string
		for _, a := range out.Result.Actions {
			b, _ := json.Marshal(a)
			ops = append(ops, string(b))
		}
		if r.code != 0 || len(ops) != 2 || !strings.HasPrefix(ops[0], `{"input":{"id":1},"operation":"complete","output":{"ok":true,`) ||
			!strings.HasPrefix(ops[1], `{"input":{"id":3},"operation":"complete","output":{"ok":true,`) {
			t.Errorf("exit %d: %s", r.code, r.stdout)
		}

		// Every target shown complete: c reopens.
		p = startPick(t, fzfDir, tr.cmd("pick", "--fields", "id", "--scope", "all"))
		p.loaded()
		p.command()
		p.send("G")
		// Ready first, then complete: 1 and 3, completed together, by ID.
		p.waitState("the cursor on the last line", func(st fzfState) bool { return lineKey(st.Current) == "3@/trips" })
		p.send("c")
		p.waitScreen("✓ reopened 3")
		p.send(keyEsc) // quits, in command mode
		if r := p.result(); r.code != 0 || !strings.Contains(r.stdout, `"operation":"reopen"`) {
			t.Errorf("exit %d: %s", r.code, r.stdout)
		}
	})
}

// e end to end: the editor gets the terminal, its keys and its screen,
// while fzf is suspended; fzf comes back with the status line, and the
// output names the notes edited. ctrl-c in the editor ends only the editor:
// Enter after it gives the envelope, with the session's actions
// (pick-spec.md, Testing: Interrupts).
func TestPickEdit(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		ed := filepath.Join(t.TempDir(), "ed")
		// It asks on the terminal, and appends the answer to each note.
		script := "#!/bin/sh\nprintf 'EDITOR> '\nread line\nfor f; do echo \"$line\" >> \"$f\"; done\n"
		if err := os.WriteFile(ed, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		pick := func(t *testing.T, env ...string) *picker {
			cmd := tr.cmd("pick", "--fields", "id")
			cmd.Env = append(append(cmd.Env, "VISUAL="+ed), env...)
			p := startPick(t, fzfDir, cmd)
			p.loaded()
			p.command()
			return p
		}
		notesOf := func(id string) string {
			b, _ := os.ReadFile(filepath.Join(tr.root(), "trips", id+".md"))
			return string(b)
		}

		t.Run("edit", func(t *testing.T) {
			p := pick(t)
			p.send("G ") // mark 3
			p.waitState("3 marked", func(st fzfState) bool { return len(st.Selected) == 1 })
			p.send("j ") // and 1
			p.waitState("1 marked", func(st fzfState) bool { return len(st.Selected) == 2 })
			p.send("e")
			p.waitScreen("EDITOR>")
			p.send("hello\r")
			p.waitScreen("✓ edited notes 2: 1, 3")
			if notesOf("1") != "hello\n" || notesOf("3") != "hello\n" {
				t.Errorf("notes %q %q", notesOf("1"), notesOf("3"))
			}
			p.waitScreen("hello") // the preview, reloaded
			p.send(keyEnter)
			r := p.result()
			if out := decode(t, r); r.code != 0 || !slices.Equal(out.Result.NotesEdited, []int64{1, 3}) {
				t.Errorf("exit %d: %s", r.code, r.stdout)
			}
		})

		t.Run("ctrl-c in the editor", func(t *testing.T) {
			// No preview, so the status line has the width.
			p := pick(t, "KOAN_PICK_OPTS=--preview-window=hidden")
			p.send("c") // complete 1, an action to report
			p.waitScreen("✓ completed 1")
			p.waitState("the reload", func(st fzfState) bool { return st.TotalCount == 2 && !st.Reading })
			p.send("e")
			p.waitScreen("EDITOR>")
			p.send(keyCtrlC)
			p.waitScreen("✗ e: editor exited with status 130")
			if p.done() {
				t.Fatal("ctrl-c in the editor ended pick")
			}
			p.send(keyEnter)
			r := p.result()
			out := decode(t, r)
			if r.code != 0 || len(out.Result.Actions) != 1 || len(out.Result.NotesEdited) != 0 {
				t.Errorf("exit %d: %s", r.code, r.stdout)
			}
		})

		t.Run("vi", func(t *testing.T) {
			if _, err := exec.LookPath("vi"); err != nil {
				t.Skip("no vi")
			}
			cmd := tr.cmd("pick", "--fields", "id")
			cmd.Env = append(cmd.Env, "EDITOR=vi")
			p := startPick(t, fzfDir, cmd)
			p.loaded()
			p.command()
			p.send("e")
			p.waitFor("vi", func() bool { return !strings.Contains(p.screen(), "[cmd]") })
			p.send("ofrom vi\x1b")
			p.send(":wq\r")
			p.waitScreen("✓ edited notes 2")
			p.send(keyEsc)
			if r := p.result(); r.code != 0 || !strings.Contains(r.stdout, `"notes_edited":[2]`) {
				t.Errorf("exit %d: %s", r.code, r.stdout)
			}
		})
	})
}

// s and r end to end: s cycles the scope and reloads, and the prompt names
// it; r finds a task another process created.
func TestPickScopeAndReload(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		if r := run(t, tr.cmd("complete", "3")); r.code != 0 {
			t.Fatal(r.stdout)
		}
		p := startPick(t, fzfDir, tr.cmd("pick", "--fields", "id"))
		if st := p.loaded(); st.TotalCount != 2 {
			t.Fatalf("open: %+v", st)
		}
		p.command()
		p.send("s")
		p.waitScreen("✓ scope: all")
		p.waitState("all three", func(st fzfState) bool { return st.TotalCount == 3 })
		p.send("i")
		p.waitScreen("all> ")
		p.command()

		if r := run(t, tr.cmd("create", "Buy adapter", "--folder", "/trips")); r.code != 0 {
			t.Fatal(r.stdout)
		}
		p.send("r")
		p.waitScreen("✓ reloaded")
		p.waitState("the new task", func(st fzfState) bool { return st.TotalCount == 4 })
		p.waitScreen("Buy adapter")
		p.send(keyEsc)
		if r := p.result(); r.code != 0 || !strings.Contains(r.stdout, `"actions":[]`) {
			t.Errorf("exit %d: %s", r.code, r.stdout)
		}
	})
}

// n end to end: the prompt borrows the query line, starting with the
// query, and the list stays put while the title is typed; Enter creates
// the task, clears the query, and puts the cursor on the new task. A
// refused title keeps the prompt open; Esc cancels it, and the search
// query comes back.
func TestPickNew(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		p := startPick(t, fzfDir, tr.cmd("pick", "--fields", "id"))
		p.loaded()
		p.setQuery("pa", 2)
		p.command()
		p.send("n")
		p.waitScreen("new> pa")
		p.waitScreen("[new]")
		p.send("ck snacks")
		st := p.waitState("the title typed", func(st fzfState) bool { return st.Query == "pack snacks" })
		if st.MatchCount != 2 {
			t.Errorf("the list moved while typing the title: %+v", st)
		}
		p.send(keyEnter)
		p.waitScreen("✓ created 4")
		p.waitState("the cursor on the new task", func(st fzfState) bool {
			return st.Query == "" && st.TotalCount == 4 && lineKey(st.Current) == "4@/"
		})
		p.waitScreen("[cmd]")
		inCommandMode := func(query string) {
			t.Helper()
			// The input hides on the reload's load event, just after the
			// prompt closes; then unbound keys type nothing, and ? after
			// them, showing the keys, shows they were handled.
			p.waitFor("the input hidden", func() bool { return !strings.Contains(p.screen(), "> ") })
			p.send("zw?")
			p.waitScreen("until the cursor moves")
			if q := p.state().Query; q != query {
				t.Errorf("query %q in command mode, want %q", q, query)
			}
		}
		inCommandMode("")

		// A refused title: the prompt stays open; Esc cancels it, and the
		// search query comes back. ctrl-d on the empty prompt first deletes
		// nothing and keeps it open: fzf's default would end the session.
		p.send("i")
		p.waitFor("insert mode", func() bool { return !strings.Contains(p.screen(), "[cmd]") })
		p.send("pa")
		p.waitState("the query", func(st fzfState) bool { return st.Query == "pa" && st.MatchCount == 3 })
		p.command()
		p.send("n")
		p.waitScreen("new> pa")
		p.send(keyCtrlU)
		p.waitState("the prompt emptied", func(st fzfState) bool { return st.Query == "" })
		p.send(keyCtrlD + "?")
		p.waitState("ctrl-d handled", func(st fzfState) bool { return st.Query == "?" })
		p.waitScreen("new> ?") // the state comes before the redraw
		if p.done() || !strings.Contains(p.screen(), "new> ?") {
			t.Fatalf("ctrl-d ended the prompt:\n%s", p.screen())
		}
		p.send(keyCtrlU)
		p.waitState("the prompt cleared", func(st fzfState) bool { return st.Query == "" })
		p.send(keyEnter)
		p.waitScreen("✗ create: invalid-input")
		if !strings.Contains(p.screen(), "new> ") {
			t.Errorf("prompt closed:\n%s", p.screen())
		}
		p.send(keyEsc)
		p.waitScreen("[cmd] query: pa")
		p.waitState("the search query back", func(st fzfState) bool { return st.Query == "pa" && st.MatchCount == 3 })
		inCommandMode("pa")
		p.send(keyEsc)
		r := p.result()
		out := decode(t, r)
		if r.code != 0 || len(out.Result.Actions) != 2 || len(out.Result.Tasks) != 0 {
			t.Errorf("exit %d: %s", r.code, r.stdout)
		}
	})
}

// Text from data in the status line is shown literally and never run: here
// an error whose message holds the root path, which holds fzf action syntax
// and a newline (pick-spec.md, Testing: Hostile text).
func TestPickHostileStatus(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads unreadable files")
	}
	eachFzf(t, func(t *testing.T, fzfDir string) {
		// A short home, so the message fits the status line: /tmp, not
		// $TMPDIR, which on macOS is a long path under /var/folders.
		home, err := os.MkdirTemp("/tmp", "h")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(filepath.Join(home, "r"), 0o755); os.RemoveAll(home) })
		// The spec's )+execute-silent(touch X)+( leaves a ( that the rest
		// of the message can't close into an action, so fzf would refuse
		// the whole chain; this one makes a chain fzf would run.
		hostile := ")+execute-silent(touch X)+change-footer("
		init := koan(t, "init", filepath.Join(home, hostile+"\nr"))
		init.Env = []string{"HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "PATH=" + os.Getenv("PATH")}
		if r := run(t, init); r.code != 0 {
			t.Fatal(r.stdout)
		}
		tr := &tree{t: t, env: init.Env, home: home}
		if r := run(t, tr.cmd("create", "one", "--notes", "secret")); r.code != 0 {
			t.Fatal(r.stdout)
		}
		notes := filepath.Join(home, hostile+"\nr", "1.md")
		if err := os.Chmod(notes, 0); err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		cmd := tr.cmd("pick")
		cmd.Dir = dir
		cmd.Env = append(cmd.Env, "KOAN_PICK_OPTS=--preview-window=hidden")
		// Checked however the test ends.
		t.Cleanup(func() {
			for _, d := range []string{dir, home} {
				if _, err := os.Stat(filepath.Join(d, "X")); !os.IsNotExist(err) {
					t.Errorf("hostile text ran, in %s: %v", d, err)
				}
			}
		})
		p := startPick(t, fzfDir, cmd)
		p.loaded()
		p.command()
		p.send("e")
		p.waitScreen("✗ e: open " + home + "/" + hostile + `\nr/1.md: permission denied`)
		p.send(keyEsc)
		if r := p.result(); r.code != 0 {
			t.Errorf("exit %d: %s", r.code, r.stdout)
		}
	})
}

// p end to end, the first prompt on targets: it starts with the target's
// priority, a refused value keeps it open, and the line shows the new one;
// on several targets it starts empty, and null clears them all.
func TestPickPriority(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		if r := run(t, tr.cmd("update", "1", "--priority", "2")); r.code != 0 {
			t.Fatal(r.stdout)
		}
		p := startPick(t, fzfDir, tr.cmd("pick", "--fields", "id,priority"))
		p.loaded()
		p.command()
		p.send("p")
		p.waitScreen("priority 1> 2")
		p.send(keyCtrlU + "high" + keyEnter)
		p.waitScreen("✗ priority 1: invalid-input")
		p.waitScreen("priority 1> high")
		p.send(keyCtrlU + "5" + keyEnter)
		p.waitScreen("✓ set priority 1")
		p.waitScreen("p5")

		// Every line marked: several, empty to start; null clears. Marks
		// wait for the reload: fzf drops those made before its list is in.
		p.waitFor("the input hidden", func() bool { return !strings.Contains(p.screen(), "> ") })
		p.post("select-all")
		p.waitState("three marked", func(st fzfState) bool { return len(st.Selected) == 3 })
		p.send("p")
		p.waitScreen("priority 3 tasks> ")
		p.send("null" + keyEnter)
		p.waitScreen("✓ set priority 3: 1, 2, 3")
		p.waitFor("p5 gone", func() bool { return !strings.Contains(p.screen(), "p5") })
		p.send(keyEnter)
		r := p.result()
		out := decode(t, r)
		if r.code != 0 || len(out.Result.Actions) != 5 || !strings.Contains(r.stdout, `"tasks":[{"id":1,"priority":null}]`) {
			t.Errorf("exit %d: %s", r.code, r.stdout)
		}
	})
}

// t end to end: it starts with the target's tags; a mix of bare and
// prefixed tags is refused with the prompt kept, as is a tag update
// refuses; bare tags then replace them, shown on the line.
func TestPickTags(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		if r := run(t, tr.cmd("update", "1", "--tags-add", "travel")); r.code != 0 {
			t.Fatal(r.stdout)
		}
		p := startPick(t, fzfDir, tr.cmd("pick", "--fields", "id,tags", "--query", "book"))
		p.loaded()
		p.command()
		p.send("t")
		p.waitScreen("tags 1> travel")
		p.send(" +urgent" + keyEnter)
		p.waitScreen("✗ tags: a mix of bare and +/- tags")
		p.waitScreen("tags 1> travel +urgent")
		p.send(keyCtrlU + "Urgent" + keyEnter)
		p.waitScreen("✗ tags 1: invalid-input")
		p.send(keyCtrlU + "travel,urgent" + keyEnter)
		p.waitScreen("✓ set tags 1")
		p.waitScreen("#travel #urgent")
		// The search query came back, and still filters.
		if st := p.state(); st.Query != "book" || st.MatchCount != 1 {
			t.Errorf("query after the prompt: %+v", st)
		}
		p.send(keyEnter)
		if r := p.result(); r.code != 0 || !strings.Contains(r.stdout, `"tasks":[{"id":1,"tags":["travel","urgent"]}]`) {
			t.Errorf("exit %d: %s", r.code, r.stdout)
		}
	})
}

// x end to end: the editor gets the task as JSON; a file that isn't valid
// is kept and reopened with the edits by the next x; a valid edit is one
// update, shown on the line.
func TestPickX(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		ed := filepath.Join(t.TempDir(), "ed")
		// First run, it breaks the file; on finding it broken, it fixes
		// it and retitles the task.
		script := `#!/bin/sh
if grep -q nope "$1"; then
	sed -i.bak -e '/nope/d' -e 's/"Book flights"/"Book cheap flights"/' "$1"
else
	echo nope >> "$1"
fi
`
		if err := os.WriteFile(ed, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := tr.cmd("pick", "--fields", "id,title")
		cmd.Env = append(cmd.Env, "VISUAL="+ed, "KOAN_PICK_OPTS=--preview-window=hidden")
		p := startPick(t, fzfDir, cmd)
		p.loaded()
		p.command()
		p.send("x")
		p.waitScreen("✗ x 1: not a JSON object")
		p.send("x")
		p.waitScreen("✓ updated 1")
		p.waitScreen("Book cheap flights")
		p.send(keyEnter)
		r := p.result()
		out := decode(t, r)
		if r.code != 0 || len(out.Result.Actions) != 1 || !strings.Contains(r.stdout, `"input":{"id":1,"title":"Book cheap flights"}`) ||
			!strings.Contains(r.stdout, `"tasks":[{"id":1,"title":"Book cheap flights"}]`) {
			t.Errorf("exit %d: %s", r.code, r.stdout)
		}
	})
}

// b end to end, the first choose list: it swaps in the candidate
// blockers, which typing filters and Tab marks, through a filter; Enter
// blocks the target with them, in the list's order, and goes back to the
// task list in command mode, input hidden; a task the target already
// reaches is not offered; Esc cancels.
func TestPickBlock(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		p := startPick(t, fzfDir, tr.cmd("pick", "--fields", "id,blocked_by"))
		p.loaded()
		p.command()
		p.send("G")
		p.waitState("the cursor on 3", func(st fzfState) bool { return lineKey(st.Current) == "3@/trips" })
		p.send("b")
		p.waitScreen("blockers of 3> ")
		p.waitScreen("[blockers of 3]")
		// The cursor lands on the first line with on-load's pos(1), after
		// the list is in.
		st := p.waitState("the choose list", func(st fzfState) bool {
			return st.TotalCount == 2 && !st.Reading && lineKey(st.Current) == "~1@/trips"
		})
		if len(st.Selected) != 0 {
			t.Fatalf("choose list: %+v", st)
		}
		p.setQuery("pass", 1)
		p.send(keyTab)
		p.waitState("2 marked", func(st fzfState) bool { return len(st.Selected) == 1 })
		p.send(keyCtrlU)
		p.waitState("the filter cleared", func(st fzfState) bool { return st.Query == "" && st.MatchCount == 2 })
		p.send(keyTab)
		p.waitState("1 marked too", func(st fzfState) bool { return len(st.Selected) == 2 })
		p.send(keyEnter)
		p.waitScreen("✓ blocked 3")
		// Back on the target, which is blocked now, so last.
		p.waitState("the task list, on 3", func(st fzfState) bool {
			return st.TotalCount == 3 && len(st.Selected) == 0 && lineKey(st.Current) == "3@/trips"
		})
		p.waitFor("the input hidden", func() bool { return !strings.Contains(p.screen(), "> ") })
		p.waitScreen("◐  3")

		// 3 is blocked by 1, so 3 isn't offered to block 1; Esc cancels.
		p.send("g")
		p.waitState("the cursor on 1", func(st fzfState) bool { return lineKey(st.Current) == "1@/trips" })
		p.send("b")
		p.waitScreen("blockers of 1> ")
		p.waitState("2 alone", func(st fzfState) bool {
			return st.TotalCount == 1 && !st.Reading && lineKey(st.Current) == "~2@/trips"
		})
		p.send(keyEsc)
		p.waitState("the task list, on 1", func(st fzfState) bool { return st.TotalCount == 3 && lineKey(st.Current) == "1@/trips" })
		p.waitFor("the input hidden", func() bool { return !strings.Contains(p.screen(), "> ") })
		p.send(keyEsc)
		r := p.result()
		out := decode(t, r)
		if r.code != 0 || len(out.Result.Actions) != 1 || !strings.Contains(r.stdout, `"input":{"id":3,"blockers":[1,2]}`) {
			t.Errorf("exit %d: %s", r.code, r.stdout)
		}
	})
}

// A cycle made while b's list is open: the candidate offered when it
// opened is refused by block, which the status line says, and the failed
// call is in the output's actions.
func TestPickBlockRefused(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		p := startPick(t, fzfDir, tr.cmd("pick", "--fields", "id"))
		p.loaded()
		p.command()
		p.send("b")
		p.waitScreen("blockers of 1> ")
		p.waitState("the choose list", func(st fzfState) bool { return st.TotalCount == 2 && !st.Reading })
		if r := run(t, tr.cmd("block", "2", "--blockers", "1")); r.code != 0 {
			t.Fatal(r.stdout)
		}
		p.setQuery("pass", 1)
		p.send(keyEnter)
		p.waitScreen("✗ block 1 ← 2: conflict (acyclic)")
		p.waitFor("the input hidden", func() bool { return !strings.Contains(p.screen(), "> ") })
		p.send(keyEsc)
		r := p.result()
		out := decode(t, r)
		if r.code != 0 || len(out.Result.Actions) != 1 ||
			!strings.Contains(r.stdout, `"input":{"id":1,"blockers":[2]},"output":{"ok":false,"error":{"kind":"conflict"`) {
			t.Errorf("exit %d: %s", r.code, r.stdout)
		}
	})
}

// u end to end: a choose list of the target's blockers; the marked one is
// removed, then the last, under the cursor, and the task is ready again.
func TestPickUnblock(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		if r := run(t, tr.cmd("block", "3", "--blockers", "1,2")); r.code != 0 {
			t.Fatal(r.stdout)
		}
		p := startPick(t, fzfDir, tr.cmd("pick", "--fields", "id,blocked_by"))
		p.loaded()
		p.command()
		p.send("G")
		p.waitState("the cursor on 3", func(st fzfState) bool { return lineKey(st.Current) == "3@/trips" })
		p.send("u")
		p.waitScreen("unblock 3> ")
		p.waitState("its two blockers", func(st fzfState) bool {
			return st.TotalCount == 2 && !st.Reading && lineKey(st.Current) == "~1@/trips"
		})
		p.send(keyTab)
		p.waitState("1 marked", func(st fzfState) bool { return len(st.Selected) == 1 })
		p.send(keyEnter)
		p.waitScreen("✓ unblocked 3")
		p.waitState("back on 3", func(st fzfState) bool { return st.TotalCount == 3 && lineKey(st.Current) == "3@/trips" })
		p.waitFor("the input hidden", func() bool { return !strings.Contains(p.screen(), "> ") })
		p.waitScreen("◐  3")
		p.send("u")
		p.waitState("one blocker left", func(st fzfState) bool {
			return st.TotalCount == 1 && !st.Reading && lineKey(st.Current) == "~2@/trips"
		})
		p.send(keyEnter)
		p.waitScreen("●  3")
		p.waitFor("the input hidden", func() bool { return !strings.Contains(p.screen(), "> ") })
		p.send(keyEsc)
		r := p.result()
		out := decode(t, r)
		if r.code != 0 || len(out.Result.Actions) != 2 || !strings.Contains(r.stdout, `"input":{"id":3,"blockers":[1]}`) ||
			!strings.Contains(r.stdout, `"input":{"id":3,"blockers":[2]}`) {
			t.Errorf("exit %d: %s", r.code, r.stdout)
		}
	})
}

// m and f end to end: single-choice lists of folders, where Tab doesn't
// mark; typing filters them. m moves the task, and the cursor follows it;
// f narrows the list to the folder chosen.
func TestPickMoveAndFolder(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		for _, f := range []string{"/trips/japan", "/home"} {
			if r := run(t, tr.cmd("create-folder", f)); r.code != 0 {
				t.Fatal(r.stdout)
			}
		}
		p := startPick(t, fzfDir, tr.cmd("pick", "--fields", "id,folder"))
		p.loaded()
		p.command()
		// 3, on the last line: the cursor finds it after the move only
		// by its ID, its key changed.
		p.send("G")
		p.waitState("the cursor on 3", func(st fzfState) bool { return lineKey(st.Current) == "3@/trips" })
		p.send("m")
		p.waitScreen("move to> ")
		p.waitScreen("[move to]")
		p.waitState("the folders", func(st fzfState) bool { return st.TotalCount == 4 && !st.Reading && lineKey(st.Current) == "~/" })
		p.send(keyTab)
		p.setQuery("jap", 1)
		if st := p.state(); len(st.Selected) != 0 {
			t.Errorf("Tab marked in a single-choice list: %+v", st)
		}
		p.send(keyEnter)
		p.waitScreen("✓ moved 3")
		p.waitState("on the moved task", func(st fzfState) bool { return lineKey(st.Current) == "3@/trips/japan" })
		p.waitFor("the input hidden", func() bool { return !strings.Contains(p.screen(), "> ") })

		p.send("f")
		p.waitScreen("folder> ")
		p.waitState("the folders", func(st fzfState) bool { return st.TotalCount == 4 && !st.Reading })
		p.setQuery("jap", 1)
		p.send(keyEnter)
		p.waitScreen("✓ folder: /trips/japan")
		p.waitState("the folder's one task", func(st fzfState) bool {
			return st.TotalCount == 1 && lineKey(st.Current) == "3@/trips/japan"
		})
		// The header's scope line: the path alone, then the preview.
		header := regexp.MustCompile(`(?m)^ +/trips/japan +│`)
		p.waitFor("the header", func() bool { return header.MatchString(p.screen()) })
		p.send(keyEsc)
		r := p.result()
		out := decode(t, r)
		if r.code != 0 || len(out.Result.Actions) != 1 || !strings.Contains(r.stdout, `"input":{"id":3,"to":"/trips/japan"}`) {
			t.Errorf("exit %d: %s", r.code, r.stdout)
		}
	})
}

// f to a folder deleted while its list is open: the reload fails, and the
// header, like the list, stays in the scope folder it was, so the next
// reload works (pick-spec.md, Errors: later loads' errors).
func TestPickFolderGone(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		if r := run(t, tr.cmd("create-folder", "/gone")); r.code != 0 {
			t.Fatal(r.stdout)
		}
		p := startPick(t, fzfDir, tr.cmd("pick", "--fields", "id"))
		p.loaded()
		p.command()
		p.send("f")
		p.waitState("the folders", func(st fzfState) bool { return st.TotalCount == 3 && !st.Reading && lineKey(st.Current) == "~/" })
		p.send(keyUp)
		p.waitState("on /gone", func(st fzfState) bool { return lineKey(st.Current) == "~/gone" })
		if r := run(t, tr.cmd("delete-folder", "/gone")); r.code != 0 {
			t.Fatal(r.stdout)
		}
		p.send(keyEnter)
		p.waitScreen("✗ reload: not-found")
		p.waitState("the task list", func(st fzfState) bool { return st.TotalCount == 3 && !st.Reading })
		// The header's scope line: still /.
		header := regexp.MustCompile(`(?m)^ +/ +│`)
		p.waitFor("the header", func() bool { return header.MatchString(p.screen()) })
		if strings.Contains(p.screen(), "folder: /gone") {
			t.Errorf("status line:\n%s", p.screen())
		}
		p.waitFor("the input hidden", func() bool { return !strings.Contains(p.screen(), "> ") })
		p.send("r")
		p.waitScreen("✓ reloaded")
		p.send(keyEsc)
		if r := p.result(); r.code != 0 {
			t.Errorf("exit %d: %s", r.code, r.stdout)
		}
	})
}

// Cancelling a prompt or a choose list, by Esc or ctrl-space, goes back to
// command mode with the search query as it was, still filtering, and the
// input hidden (pick-spec.md, Modes).
func TestPickCancel(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		for _, c := range []struct{ name, key, opened, cancel string }{
			{"esc, prompt", "p", "priority 2> ", keyEsc},
			{"ctrl-space, prompt", "p", "priority 2> ", "\x00"},
			{"esc, choose list", "b", "blockers of 2> ", keyEsc},
			{"ctrl-space, choose list", "b", "blockers of 2> ", "\x00"},
		} {
			t.Run(c.name, func(t *testing.T) {
				p := startPick(t, fzfDir, tr.cmd("pick", "--fields", "id"))
				p.loaded()
				p.setQuery("pa", 2)
				p.command()
				p.send(c.key)
				p.waitScreen(c.opened)
				if c.key == "b" {
					p.waitState("the choose list", func(st fzfState) bool { return st.TotalCount == 2 && st.Query == "" && !st.Reading })
				}
				p.send(c.cancel)
				p.waitScreen("[cmd] query: pa")
				p.waitState("the search query back", func(st fzfState) bool {
					return st.Query == "pa" && st.TotalCount == 3 && st.MatchCount == 2 && !st.Reading
				})
				p.waitFor("the input hidden", func() bool { return !strings.Contains(p.screen(), "> ") })
				p.send(keyEsc)
				if got := picked(t, p.result()); len(got) != 0 {
					t.Errorf("picked %v", got)
				}
			})
		}
	})
}

// Keys typed ahead of the reload that leaving a prompt or choose list
// starts: each acts in the mode the session is in, or, passing the keys
// of the list fzf still shows, does nothing and says so. Each case sends
// the next key in the same write, so it is handled before the reload is
// in.
func TestPickTypeahead(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		start := func(t *testing.T) *picker {
			t.Helper()
			p := startPick(t, fzfDir, tr.cmd("pick", "--fields", "id,priority"))
			p.loaded()
			p.command()
			return p
		}
		// The reload's load event runs on-load just after the list is in:
		// it must not hide the input that the typed-ahead key showed.
		const settle = 500 * time.Millisecond

		t.Run("i after a prompt", func(t *testing.T) {
			p := start(t)
			p.send("p")
			p.waitScreen("priority 1> ")
			p.send(keyCtrlU + "5" + keyEnter + "i")
			p.waitScreen("✓ set priority 1")
			p.waitScreen("p5")
			p.holds("insert mode, input shown", settle, func() bool {
				s := p.screen()
				return strings.Contains(s, "open> ") && !strings.Contains(s, "[cmd]")
			})
			p.setQuery("zz", 0)
			p.send(keyCtrlC)
			p.wait()
		})

		t.Run("n after a choose list", func(t *testing.T) {
			p := start(t)
			p.send("f")
			p.waitState("the folders", func(st fzfState) bool { return st.TotalCount == 2 && !st.Reading })
			p.send(keyEnter + "n")
			p.waitScreen("[new]")
			p.waitState("the task list", func(st fzfState) bool { return st.TotalCount == 3 && !st.Reading })
			p.holds("the prompt shown", settle, func() bool { return strings.Contains(p.screen(), "new> ") })
			p.send("abc")
			p.waitState("the title typed", func(st fzfState) bool { return st.Query == "abc" })
			p.send(keyCtrlC)
			p.wait()
		})

		t.Run("enter after a choose list", func(t *testing.T) {
			p := start(t)
			p.send("b")
			p.waitState("the choose list", func(st fzfState) bool {
				return st.TotalCount == 2 && !st.Reading && lineKey(st.Current) == "~2@/trips"
			})
			// The second Enter passes the choose list's 2, which it shows,
			// not a task under the cursor: it picks nothing.
			p.send(keyEnter + keyEnter)
			p.waitScreen("list still loading")
			p.waitState("the task list, on 1", func(st fzfState) bool {
				return st.TotalCount == 3 && !st.Reading && lineKey(st.Current) == "1@/trips"
			})
			if p.done() {
				t.Fatalf("pick exited: %s", p.stdout.String())
			}
			p.send(keyEnter)
			r := p.result()
			out := decode(t, r)
			if r.code != 0 || len(out.Result.Tasks) != 1 || out.Result.Tasks[0].ID != 1 ||
				len(out.Result.Actions) != 1 || !strings.Contains(r.stdout, `"input":{"id":1,"blockers":[2]}`) {
				t.Errorf("exit %d: %s", r.code, r.stdout)
			}
		})
	})
}

// --source end to end: the command's IDs are the candidates, and every
// reload runs it again, so a task an action takes out of its output leaves
// the list, and one another process puts in joins it at r. A later run's
// stderr shows in the status line literally, never run; a failed first run
// ends pick before fzf.
func TestPickSource(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		for _, id := range []string{"1", "3"} {
			if r := run(t, tr.cmd("update", id, "--tags-add", "today")); r.code != 0 {
				t.Fatal(r.stdout)
			}
		}
		t.Run("live", func(t *testing.T) {
			source := "'" + binary + "' list --tags-any today --fields id"
			p := startPick(t, fzfDir, tr.cmd("pick", "--fields", "id", "--source", source))
			if st := p.loaded(); st.TotalCount != 2 {
				t.Fatalf("first run: %+v", st)
			}
			p.waitScreen("live source")
			p.command()
			p.send("t")
			p.waitScreen("tags 1> today")
			p.send(keyCtrlU + "later" + keyEnter)
			p.waitScreen("✓ set tags 1")
			p.waitState("1 gone from the source", func(st fzfState) bool { return st.TotalCount == 1 })
			if r := run(t, tr.cmd("update", "2", "--tags-add", "today")); r.code != 0 {
				t.Fatal(r.stdout)
			}
			p.waitFor("the input hidden", func() bool { return !strings.Contains(p.screen(), "> ") })
			p.send("r")
			p.waitScreen("✓ reloaded")
			p.waitState("2 joined", func(st fzfState) bool { return st.TotalCount == 2 })
			p.send(keyEsc)
			if r := p.result(); r.code != 0 {
				t.Errorf("exit %d: %s", r.code, r.stdout)
			}
		})

		t.Run("hostile stderr", func(t *testing.T) {
			dir := t.TempDir()
			hostile := ")+execute-silent(touch X)+change-footer("
			script := filepath.Join(dir, "source")
			body := "#!/bin/sh\nif [ -e ran ]; then\n\techo '" + hostile + "' >&2\n\techo second line >&2\n\texit 1\nfi\ntouch ran\n'" + binary + "' list --fields id\n"
			if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := os.Stat(filepath.Join(dir, "X")); !os.IsNotExist(err) {
					t.Errorf("hostile text ran: %v", err)
				}
			})
			cmd := tr.cmd("pick", "--source", script)
			cmd.Dir = dir
			cmd.Env = append(cmd.Env, "KOAN_PICK_OPTS=--preview-window=hidden")
			p := startPick(t, fzfDir, cmd)
			p.loaded()
			p.command()
			p.send("r")
			p.waitScreen("✗ source: " + hostile)
			if strings.Contains(p.screen(), "✓ reloaded") || strings.Contains(p.screen(), "second line") {
				t.Errorf("status line:\n%s", p.screen())
			}
			p.send(keyEsc)
			p.result()
		})

		// A reload that fails on leaving a prompt keeps the list, and
		// still takes fzf back to command mode, input hidden.
		t.Run("fails after a prompt", func(t *testing.T) {
			dir := t.TempDir()
			script := filepath.Join(dir, "source")
			body := "#!/bin/sh\nif [ -e ran ]; then\n\techo gone >&2\n\texit 1\nfi\ntouch ran\n'" + binary + "' list --fields id\n"
			if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := tr.cmd("pick", "--fields", "id", "--source", script)
			cmd.Dir = dir
			cmd.Env = append(cmd.Env, "KOAN_PICK_OPTS=--preview-window=hidden")
			p := startPick(t, fzfDir, cmd)
			total := p.loaded().TotalCount
			p.command()
			p.send("p")
			p.waitScreen("[priority 1]")
			p.send(keyCtrlU + "4" + keyEnter)
			p.waitScreen("✓ set priority 1 · ✗ source: gone")
			p.waitScreen("[cmd]")
			p.waitFor("the input hidden", func() bool { return !strings.Contains(p.screen(), "> ") })
			if st := p.state(); st.TotalCount != total {
				t.Errorf("list changed: %+v", st)
			}
			p.send(keyEsc)
			if r := p.result(); r.code != 0 || !strings.Contains(r.stdout, `"input":{"id":1,"priority":4}`) {
				t.Errorf("exit %d: %s", r.code, r.stdout)
			}
		})

		// A later run that takes too long is killed with its process
		// group, the list kept; the limit is shortened by a test hook.
		t.Run("timeout", func(t *testing.T) {
			dir := t.TempDir()
			script := filepath.Join(dir, "source")
			body := "#!/bin/sh\nif [ -e ran ]; then\n\tsleep 30 &\n\techo $! > bg.pid\n\tsleep 30\nfi\ntouch ran\n'" + binary + "' list --fields id\n"
			if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := tr.cmd("pick", "--source", script)
			cmd.Dir = dir
			cmd.Env = append(cmd.Env, "KOAN_E2E_SOURCE_LIMIT=300ms", "KOAN_PICK_OPTS=--preview-window=hidden")
			p := startPick(t, fzfDir, cmd)
			total := p.loaded().TotalCount
			p.command()
			start := time.Now()
			p.send("r")
			p.waitScreen("✗ source: timed out after 10s")
			if d := time.Since(start); d > 5*time.Second {
				t.Errorf("timed out after %v", d)
			}
			if st := p.state(); st.TotalCount != total {
				t.Errorf("list changed: %+v", st)
			}
			// The command's background child went with it.
			b, err := os.ReadFile(filepath.Join(dir, "bg.pid"))
			if err != nil {
				t.Fatal(err)
			}
			pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
			p.waitFor("the background child gone", func() bool { return syscall.Kill(pid, 0) == syscall.ESRCH })
			p.send(keyEsc)
			p.result()
		})

		t.Run("first run fails", func(t *testing.T) {
			p := startPick(t, fzfDir, tr.cmd("pick", "--source", "echo nope; echo 'no such thing' >&2"))
			r := p.result()
			if out := decode(t, r); r.code != 1 || out.Error == nil || out.Error.Kind != "invalid-input" ||
				!strings.Contains(r.stdout, `{"field":"/source","reason":"no such thing"}`) {
				t.Errorf("exit %d: %s", r.code, r.stdout)
			}
		})
	})
}

// --folders end to end: the folders, / first; typing filters them; marks
// emit in tree order; there are no actions; q quits; --select-one finishes
// without the picker.
func TestPickFolders(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		for _, f := range []string{"/trips/japan", "/home"} {
			if r := run(t, tr.cmd("create-folder", f)); r.code != 0 {
				t.Fatal(r.stdout)
			}
		}
		folders := func(r result) string {
			t.Helper()
			envelope(t, r)
			var res struct {
				OK     bool            `json:"ok"`
				Result json.RawMessage `json:"result"`
			}
			json.Unmarshal([]byte(r.stdout), &res)
			if r.code != 0 || !res.OK {
				t.Errorf("exit %d: %s", r.code, r.stdout)
			}
			return string(res.Result)
		}

		p := startPick(t, fzfDir, tr.cmd("pick", "--folders"))
		if st := p.loaded(); st.TotalCount != 4 || lineKey(st.Current) != "/" {
			t.Fatalf("folders: %+v", st)
		}
		p.setQuery("jap", 1)
		p.send(keyEnter)
		if got := folders(p.result()); got != `{"folders":["/trips/japan"],"missing":[],"actions":[]}` {
			t.Errorf("enter: %s", got)
		}

		// Marked /trips then /home; c is no action here.
		p = startPick(t, fzfDir, tr.cmd("pick", "--folders"))
		p.loaded()
		p.command()
		p.send("k")
		p.waitState("on /home", func(st fzfState) bool { return lineKey(st.Current) == "/home" })
		p.send("k")
		p.waitState("on /trips", func(st fzfState) bool { return lineKey(st.Current) == "/trips" })
		// Space marks, and moves down to /home.
		p.send(" ")
		p.waitState("/trips marked", func(st fzfState) bool { return len(st.Selected) == 1 && lineKey(st.Current) == "/home" })
		p.send("c")
		p.send(keyTab)
		p.waitState("/home marked", func(st fzfState) bool { return len(st.Selected) == 2 })
		p.send(keyEnter)
		if got := folders(p.result()); got != `{"folders":["/home","/trips"],"missing":[],"actions":[]}` {
			t.Errorf("marks: %s", got)
		}

		// Chosen, then deleted by another process: missing.
		p = startPick(t, fzfDir, tr.cmd("pick", "--folders"))
		p.loaded()
		p.setQuery("hom", 1)
		if r := run(t, tr.cmd("delete-folder", "/home")); r.code != 0 {
			t.Fatal(r.stdout)
		}
		p.send(keyEnter)
		if got := folders(p.result()); got != `{"folders":[],"missing":["/home"],"actions":[]}` {
			t.Errorf("missing: %s", got)
		}

		p = startPick(t, fzfDir, tr.cmd("pick", "--folders"))
		p.loaded()
		p.command()
		p.send("q")
		if got := folders(p.result()); got != `{"folders":[],"missing":[],"actions":[]}` {
			t.Errorf("q: %s", got)
		}

		p = startPick(t, fzfDir, tr.cmd("pick", "--folders", "--select-one", "--query", "japan"))
		if got := folders(p.result()); got != `{"folders":["/trips/japan"],"missing":[],"actions":[]}` {
			t.Errorf("select-one: %s", got)
		}
	})
}

// pick in a pipeline: --from reads an upstream envelope, of each accepted
// shape, while the picker draws on the terminal; stdout goes downstream.
// The rejections end pick before the picker opens (pick-spec.md, Accepted
// envelopes).
func TestPickPipelines(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		if r := run(t, tr.cmd("complete", "3")); r.code != 0 {
			t.Fatal(r.stdout)
		}
		envOf := func(args ...string) string {
			r := run(t, tr.cmd(args...))
			if r.code != 0 {
				t.Fatal(r.stdout)
			}
			return r.stdout
		}
		listed := envOf("list", "--readiness", "ready,complete", "--fields", "id")
		accepted := []struct {
			name, upstream string
			lines          int // candidates
		}{
			{"list", listed, 3},
			{"frontier", envOf("frontier", "--fields", "id,title"), 2},
			{"show", envOf("show", "2"), 1},
			{"single task", envOf("update", "1", "--priority", "1"), 1},
			{"empty tasks", envOf("list", "--tags-any", "none", "--fields", "id"), 0},
		}
		for _, a := range accepted {
			t.Run(a.name, func(t *testing.T) {
				p := startPick(t, fzfDir, stdin(tr.cmd("pick", "--from", "-", "--fields", "id"), a.upstream))
				st := p.loaded()
				if st.TotalCount != a.lines {
					t.Fatalf("%d lines, want %d; screen:\n%s", st.TotalCount, a.lines, p.screen())
				}
				p.quit()
				if got := picked(t, p.result()); len(got) != 0 {
					t.Errorf("picked %v", got)
				}
			})
		}
		// A shell pipeline: list into pick into a second pick, which
		// narrows the first's selection, with both pickers on the one
		// terminal in turn; the result goes to a file.
		t.Run("two passes", func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out.json")
			ft := exec.Command("sh", "-c", fmt.Sprintf(
				"%[1]q list --fields id | %[1]q pick --from - | %[1]q pick --from - --fields id,title > %[2]q", binary, out))
			ft.Env = tr.env
			p := startPick(t, fzfDir, ft)
			if st := p.loaded(); st.TotalCount != 2 {
				t.Fatalf("first pass: %+v", st)
			}
			p.send(keyTab + keyUp + keyTab)
			p.waitState("both marked", func(st fzfState) bool { return len(st.Selected) == 2 })
			p.send(keyEnter)
			// The second picker has the first's selection, on the same port.
			p.waitFor("the second pass", func() bool {
				st, err := p.get()
				return err == nil && st.TotalCount == 2 && len(st.Selected) == 0 && lineKey(st.Current) == "1@/trips"
			})
			p.send(keyUp + keyEnter)
			if code := p.wait(); code != 0 {
				t.Fatalf("exit %d: %s", code, p.stderr.String())
			}
			b, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), `"tasks":[{"id":2,"title":"Renew passport"}]`) {
				t.Errorf("out: %s", b)
			}
		})
		rejected := []struct {
			name, upstream, want string
		}{
			{"not JSON", "nope", `"field":"/ids"`},
			{"two values", envOf("show", "1") + envOf("show", "2"), `"field":"/ids"`},
			{"ok false", run(t, tr.cmd("show", "99")).stdout, `not-found`},
			{"no tasks", `{"ok":true,"result":{"folders":["/"]},"warnings":[]}`, `"field":"/ids"`},
		}
		for _, rj := range rejected {
			t.Run("rejects "+rj.name, func(t *testing.T) {
				p := startPick(t, fzfDir, stdin(tr.cmd("pick", "--from", "-"), rj.upstream))
				r := p.result()
				out := decode(t, r)
				if r.code != 1 || out.Error == nil || out.Error.Kind != "invalid-input" || !strings.Contains(r.stdout, rj.want) {
					t.Errorf("exit %d: %s", r.code, r.stdout)
				}
			})
		}
	})
}

// A final read that fails after the picker closes gives incomplete, with
// the read's error and the actions (pick-spec.md, Output).
func TestPickFinalReadFails(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		p := startPick(t, fzfDir, tr.cmd("pick"))
		p.loaded()
		if err := os.Rename(tr.root(), tr.root()+".gone"); err != nil {
			t.Fatal(err)
		}
		p.send(keyEnter)
		r := p.result()
		out := decode(t, r)
		if r.code != 1 || out.Error == nil || out.Error.Kind != "incomplete" || out.Error.Details.Error == nil ||
			out.Error.Details.Actions == nil || len(out.Error.Details.Actions) != 0 {
			t.Errorf("exit %d: %s", r.code, r.stdout)
		}
	})
}

// ctrl-d on an empty prompt deletes nothing and keeps the session: it is
// delete-char, never fzf's default abort or a delete.
func TestPickCtrlD(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		before := run(t, tr.cmd("list", "--readiness", "ready,blocked,complete")).stdout
		p := startPick(t, fzfDir, tr.cmd("pick", "--fields", "id"))
		p.loaded()
		p.send(keyCtrlD + keyCtrlD)
		// A key after them shows they were handled, and fzf is still up.
		p.setQuery("pack", 1)
		if p.done() {
			t.Fatal("ctrl-d ended pick")
		}
		p.send(keyEnter)
		if got := picked(t, p.result()); !slices.Equal(got, []int64{3}) {
			t.Errorf("picked %v", got)
		}
		if after := run(t, tr.cmd("list", "--readiness", "ready,blocked,complete")).stdout; after != before {
			t.Errorf("tree changed:\n%s\n%s", before, after)
		}
	})
}

// The first load: fzf may or may not run on-load for the first list, but
// the picker always opens with the cursor on the first line. The cursor is
// checked once Esc's callback has run, after any on-load for the first
// list, which fzf runs one action at a time; the helper's log says whether
// it ran.
func TestPickFirstLoadCursor(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		ran := 0
		for i := range 20 {
			log := filepath.Join(t.TempDir(), "helper.log")
			cmd := tr.cmd("pick")
			cmd.Env = append(cmd.Env, "KOAN_E2E_HELPER_LOG="+log)
			p := startPick(t, fzfDir, cmd)
			p.loaded()
			p.command()
			if st := p.state(); st.Current == nil || st.Current.Index != 0 || lineKey(st.Current) != "1@/trips" {
				t.Fatalf("run %d: cursor on %+v", i, st.Current)
			}
			b, _ := os.ReadFile(log)
			if slices.Contains(strings.Split(string(b), "\n"), "on-load") {
				ran++
			}
			p.send(keyCtrlC)
			p.wait()
		}
		t.Logf("on-load ran for the first list in %d of 20 runs", ran)
	})
}

// FZF_DEFAULT_OPTS='--tmux' inside tmux still runs fzf in the terminal,
// tmux's pane, rather than in a popup, which has a terminal of its own
// (pick-spec.md, fzf options). A callback bound through the options says
// which terminal fzf runs on; plain fzf --tmux is the control.
func TestPickTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux installed")
	}
	if os.Geteuid() == 0 {
		t.Skip("root's shell prompt is #, not the $ waited for")
	}
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		dir := t.TempDir()
		sock := filepath.Join(dir, "sock")
		ttyBind := func(file string) string {
			return fmt.Sprintf("--bind 'ctrl-t:execute-silent(ps -o tty= -p $$ > %s)'", file)
		}
		env, port := pickEnv(t, fzfDir, append(tr.env, "FZF_DEFAULT_OPTS=--tmux", "PS1=$ ", "KOAN_PICK_OPTS="+ttyBind("pick.tty")))
		cmd := exec.Command("tmux", "-S", sock, "-f", "/dev/null", "new-session", "-x", "100", "-y", "24", "sh")
		cmd.Env, cmd.Dir = env, dir
		p := &picker{term: startTerm(t, cmd, 24, 100), port: port}
		t.Cleanup(func() { exec.Command("tmux", "-S", sock, "kill-server").Run() })
		p.waitScreen("$")
		out, err := exec.Command("tmux", "-S", sock, "display", "-p", "#{pane_tty}").Output()
		if err != nil {
			t.Fatal(err)
		}
		pane := strings.TrimSpace(string(out))
		// ttyOf presses ctrl-t and returns the terminal the callback ran on.
		ttyOf := func(file string) string {
			t.Helper()
			p.send("\x14")
			var b []byte
			p.waitFor(file, func() bool { b, _ = os.ReadFile(filepath.Join(dir, file)); return len(b) > 0 })
			return strings.TrimSpace(string(b))
		}

		p.send("echo Popup | fzf " + ttyBind("control.tty") + " > /dev/null\r")
		p.waitScreen("Popup")
		if got := ttyOf("control.tty"); strings.HasSuffix(pane, "/"+got) {
			t.Fatalf("control: fzf --tmux ran on the pane's terminal, %s", pane)
		}
		p.send(keyCtrlC)
		// Keys typed before the popup closes go to it.
		p.waitFor("the prompt back", func() bool {
			n := 0
			for _, l := range strings.Split(p.screen(), "\n") {
				if strings.HasSuffix(l, "$") || strings.Contains(l, "$ ") {
					n++
				}
			}
			return n == 2
		})

		p.send(fmt.Sprintf("%q pick --fields id > out.json; echo $? > rc\r", binary))
		p.listening()
		p.loaded()
		if got := ttyOf("pick.tty"); !strings.HasSuffix(pane, "/"+got) {
			t.Errorf("pick's fzf ran on %s, not the pane's %s", got, pane)
		}
		p.send(keyEnter)
		rc := filepath.Join(dir, "rc")
		p.waitFor("pick to exit", func() bool { b, _ := os.ReadFile(rc); return len(b) > 0 })
		b, _ := os.ReadFile(rc)
		res, _ := os.ReadFile(filepath.Join(dir, "out.json"))
		if strings.TrimSpace(string(b)) != "0" || !strings.Contains(string(res), `"tasks":[{"id":1}]`) {
			t.Errorf("exit %s: %s", b, res)
		}
	})
}
