package pick

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/ftask/internal/fsys"
	"github.com/phansen314/ftask/internal/jsonio"
	"github.com/phansen314/ftask/internal/model"
	"github.com/phansen314/ftask/internal/ops"
)

func TestPreviews(t *testing.T) {
	flights := tv{id: 42, folder: "/trips/japan", r: model.Ready, priority: p(2), tags: []model.Tag{"travel"}, title: "Book flights"}.view()
	flights.CreatedAt, flights.UpdatedAt, flights.NotesPath = "2026-09-20T18:31:51Z", "2026-09-21T08:00:00Z", "/r/trips/japan/42.md"
	flights.Extra = &jsonio.Object{}
	flights.Extra.Set("status", "waiting on quote")
	flights.Extra.Set("n", []any{1, "x"})
	flights.Extra.Set("bad\x1bkey", "line\nbreak\u0085")
	hotel := tv{id: 43, folder: "/trips/japan", r: model.Blocked, blocking: []model.ID{42}, title: "Book hotel"}.view()
	hotel.BlockedBy = []model.ID{7, 42}
	// A duplicated ID blocking 42 counts once; a complete task blocks nothing.
	copyA := tv{id: 44, folder: "/a", r: model.Blocked, title: "copy"}.view()
	copyA.BlockedBy = []model.ID{42}
	copyB := tv{id: 44, folder: "/b", r: model.Blocked, title: "copy"}.view()
	copyB.BlockedBy = []model.ID{42}
	done := tv{id: 9, folder: "/trips", r: model.Complete, done: "2026-09-25T10:00:00Z", title: "Renew passport"}.view()
	done.BlockedBy = []model.ID{42}

	got := previews(&Load{Tasks: []model.TaskView{flights, hotel, copyA, copyB, done}})
	if want := (previewEntry{
		Left:  "#42 Book flights",
		Right: "ready  p2",
		Lines: []string{
			"/trips/japan   #travel   created 2026-09-20   updated 2026-09-21",
			"blocked by: —   blocks: 43, 44",
			"extra: status = waiting on quote",
			`       n = [1,"x"]`,
			`       bad\x1bkey = line\nbreak\u0085`,
		},
		Notes: "/r/trips/japan/42.md",
	}); !equalEntry(got["42@/trips/japan"], want) {
		t.Errorf("got %#v", got["42@/trips/japan"])
	}
	if l := got["43@/trips/japan"].Lines[1]; l != "blocked by: 7, 42   blocks: —" {
		t.Errorf("hotel: %q", l)
	}
	d := got["9@/trips"]
	if d.Right != "complete" || !strings.HasSuffix(d.Lines[0], "completed 2026-09-25") {
		t.Errorf("complete: %#v", d)
	}
	if len(got) != 5 {
		t.Errorf("%d previews, want one per copy", len(got))
	}
}

func equalEntry(a, b previewEntry) bool {
	return a.Left == b.Left && a.Right == b.Right && a.Notes == b.Notes && slices.Equal(a.Lines, b.Lines)
}

// fakeTools is a System with glow and bat as given ("" for not on PATH).
type fakeTools struct {
	environ   []string
	glow, bat string
	fail      map[string]bool // tool names that exit 1
	calls     [][]string      // tool, args..., then env entries prefixed "env:"
}

func (f *fakeTools) system() System {
	return System{
		Environ: func() []string { return f.environ },
		LookPath: func(file string) (string, error) {
			switch {
			case file == "glow" && f.glow != "":
				return f.glow, nil
			case file == "bat" && f.bat != "":
				return f.bat, nil
			}
			return "", errors.New("not found")
		},
		Output: func(path string, args, env []string) ([]byte, []byte, int, error) {
			call := append([]string{filepath.Base(path)}, args...)
			for _, kv := range env {
				call = append(call, "env:"+kv)
			}
			f.calls = append(f.calls, call)
			if f.fail[filepath.Base(path)] {
				return nil, nil, 1, nil
			}
			return []byte("[" + filepath.Base(path) + "]\n"), nil, 0, nil
		},
	}
}

func TestPreviewVerb(t *testing.T) {
	s, e := newSession(fsys.OS{}, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Remove()
	dir := t.TempDir()
	notesPath := filepath.Join(dir, "1.md")
	run := func(f *fakeTools, args ...string) string {
		t.Helper()
		out, e := previewVerb(s, args, Env{Ops: ops.Env{}, Sys: f.system()}.withFS())
		if e != nil {
			t.Fatal(e)
		}
		return string(out)
	}

	// No previews yet, or unreadable ones: loading….
	if got := run(&fakeTools{}, "1@/"); got != "loading…\n" {
		t.Errorf("no file: %q", got)
	}
	s.Write(previewsFile, []byte("{not json"))
	if got := run(&fakeTools{}, "1@/"); got != "loading…\n" {
		t.Errorf("bad file: %q", got)
	}

	v := tv{id: 1, folder: "/", r: model.Ready, title: "日本 trip"}.view()
	v.CreatedAt, v.UpdatedAt, v.NotesPath = "2026-09-20T00:00:00Z", "2026-09-20T00:00:00Z", notesPath
	if e := writePreviews(s, &Load{Tasks: []model.TaskView{v}}); e != nil {
		t.Fatal(e)
	}
	if got := run(&fakeTools{}, "2@/"); got != "loading…\n" {
		t.Errorf("unknown key: %q", got)
	}
	// A blocker with no task, keyed by its ID alone.
	if got := run(&fakeTools{}, "50"); got != "no task with ID 50\n" {
		t.Errorf("no such task: %q", got)
	}

	// Missing and empty notes; the first line fills the pane's width.
	plain := &fakeTools{environ: []string{"FZF_PREVIEW_COLUMNS=30"}}
	want := "#1 日本 trip             ready\n" +
		"/   created 2026-09-20   updated 2026-09-20\n" +
		"blocked by: —   blocks: —\n" +
		"── notes ─────────────────────\n" +
		"(no notes)\n"
	if got := run(plain, "1@/"); got != want {
		t.Errorf("missing notes:\n%q\nwant\n%q", got, want)
	}
	os.WriteFile(notesPath, nil, 0o644)
	if got := run(plain, "1@/"); !strings.HasSuffix(got, "(no notes)\n") {
		t.Errorf("empty notes: %q", got)
	}

	// Notes as they are, without a renderer.
	os.WriteFile(notesPath, []byte("# Plan\nno newline"), 0o644)
	if got := run(plain, "1@/"); !strings.HasSuffix(got, "─\n# Plan\nno newline\n") {
		t.Errorf("plain notes: %q", got)
	}

	// glow first, in color, forced since its stdout is a pipe.
	f := &fakeTools{glow: "/bin/glow", bat: "/bin/bat", environ: []string{"FZF_PREVIEW_COLUMNS=30"}}
	if got := run(f, "1@/"); !strings.HasSuffix(got, "[glow]\n") || len(f.calls) != 1 ||
		!slices.Equal(f.calls[0], []string{"glow", "-s", "dark", "-w", "30", notesPath, "env:FZF_PREVIEW_COLUMNS=30", "env:CLICOLOR_FORCE=1"}) {
		t.Errorf("glow: %q, calls %q", got, f.calls)
	}
	f = &fakeTools{glow: "/bin/glow", environ: []string{"GLAMOUR_STYLE=light"}}
	run(f, "1@/")
	if f.calls[0][2] != "light" {
		t.Errorf("GLAMOUR_STYLE: %q", f.calls)
	}
	f = &fakeTools{glow: "/bin/glow", environ: []string{"NO_COLOR=1"}}
	run(f, "1@/")
	if f.calls[0][2] != "notty" {
		t.Errorf("NO_COLOR, glow: %q", f.calls)
	}

	// bat when there is no glow, or glow fails; never color with NO_COLOR.
	f = &fakeTools{glow: "/bin/glow", bat: "/bin/bat", fail: map[string]bool{"glow": true}}
	if got := run(f, "1@/"); !strings.HasSuffix(got, "[bat]\n") || len(f.calls) != 2 ||
		!slices.Equal(f.calls[1], []string{"bat", "--color=always", "--style=plain", "--language=markdown", "--paging=never", "--terminal-width", "80", notesPath}) {
		t.Errorf("bat: %q, calls %q", got, f.calls)
	}
	f = &fakeTools{bat: "/bin/bat", environ: []string{"NO_COLOR=1"}}
	run(f, "1@/")
	if f.calls[0][1] != "--color=never" {
		t.Errorf("NO_COLOR, bat: %q", f.calls)
	}
	// Both fail: as it is.
	f = &fakeTools{glow: "/bin/glow", bat: "/bin/bat", fail: map[string]bool{"glow": true, "bat": true}}
	if got := run(f, "1@/"); !strings.HasSuffix(got, "no newline\n") {
		t.Errorf("both fail: %q", got)
	}

	for _, args := range [][]string{nil, {"1@/", "x"}} {
		if _, e := previewVerb(s, args, Env{Sys: plain.system()}); e == nil {
			t.Errorf("%q: no usage error", args)
		}
	}
}

// withFS gives the operations' environment the real filesystem.
func (e Env) withFS() Env {
	e.Ops.FS = fsys.OS{}
	return e
}
