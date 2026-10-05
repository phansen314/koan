package pick

import (
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/ops"
)

// tv is a task view for tests: id in folder, with readiness r.
type tv struct {
	id       model.ID
	folder   string
	r        model.Readiness
	priority *int64
	done     string // completed_at, for complete
	tags     []model.Tag
	blocking []model.ID
	title    string
}

func (t tv) view() model.TaskView {
	v := model.TaskView{Readiness: t.r, Blocking: t.blocking}
	v.ID, v.Folder, v.Priority, v.Tags = t.id, model.FolderPath(t.folder), t.priority, t.tags
	v.Title = model.Title(t.title)
	if v.Title == "" {
		v.Title = "task"
	}
	if t.r == model.Complete {
		ts := model.Timestamp(t.done)
		v.CompletedAt = &ts
	}
	return v
}

func views(ts ...tv) []model.TaskView {
	out := make([]model.TaskView, len(ts))
	for i, t := range ts {
		out[i] = t.view()
	}
	return out
}

func keys(vs []model.TaskView) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = key(v)
	}
	return out
}

func p(n int64) *int64 { return &n }

func TestCandidatesOrder(t *testing.T) {
	// Given in tree order; listed ready, blocked, complete.
	l := &Load{Folders: []model.FolderPath{"/", "/a", "/b"}, Tasks: views(
		tv{id: 1, folder: "/", r: model.Complete, done: "2026-09-01T00:00:00Z"},
		tv{id: 2, folder: "/", r: model.Blocked, blocking: []model.ID{9}},
		tv{id: 3, folder: "/", r: model.Ready},
		tv{id: 4, folder: "/", r: model.Ready, priority: p(-1)},
		tv{id: 5, folder: "/", r: model.Ready, priority: p(2)},
		tv{id: 6, folder: "/", r: model.Blocked, priority: p(1), blocking: []model.ID{9}},
		tv{id: 7, folder: "/", r: model.Complete, done: "2026-09-02T00:00:00Z"},
		tv{id: 8, folder: "/", r: model.Complete, done: "2026-09-01T00:00:00Z"},
		tv{id: 0, folder: "/", r: model.Ready, priority: p(2)},
		// A duplicated ID: its copies in tree order.
		tv{id: 10, folder: "/b", r: model.Ready},
		tv{id: 10, folder: "/a", r: model.Ready},
	)}
	got, missing := l.candidates(Scope{Folder: "/", Recursive: true, Readiness: ops.PickAll})
	want := []string{"0@/", "5@/", "4@/", "3@/", "10@/a", "10@/b", "6@/", "2@/", "7@/", "1@/", "8@/"}
	if !slices.Equal(keys(got), want) || missing != 0 {
		t.Errorf("got %q, %d missing; want %q", keys(got), missing, want)
	}
}

func TestCandidatesScope(t *testing.T) {
	l := &Load{Folders: []model.FolderPath{"/", "/a", "/a/b", "/ab"}, Tasks: views(
		tv{id: 1, folder: "/", r: model.Ready, tags: []model.Tag{"x"}},
		tv{id: 2, folder: "/a", r: model.Ready, tags: []model.Tag{"x", "y"}},
		tv{id: 3, folder: "/a/b", r: model.Blocked, tags: []model.Tag{"y"}},
		tv{id: 4, folder: "/ab", r: model.Complete, done: "2026-09-01T00:00:00Z"},
		tv{id: 5, folder: "/a", r: model.Complete, done: "2026-09-01T00:00:00Z", tags: []model.Tag{"x"}},
	)}
	all := Scope{Folder: "/", Recursive: true, Readiness: ops.PickAll}
	with := func(f func(*Scope)) Scope { s := all; f(&s); return s }
	for _, tc := range []struct {
		name    string
		s       Scope
		want    string
		missing int
	}{
		{"everything", all, "1 2 3 4 5", 0},
		{"open", with(func(s *Scope) { s.Readiness = ops.PickOpen }), "1 2 3", 0},
		{"ready", with(func(s *Scope) { s.Readiness = ops.PickReady }), "1 2", 0},
		{"folder", with(func(s *Scope) { s.Folder = "/a" }), "2 3 5", 0},
		{"not a prefix", with(func(s *Scope) { s.Folder = "/ab" }), "4", 0},
		{"not recursive", with(func(s *Scope) { s.Folder = "/a"; s.Recursive = false }), "2 5", 0},
		{"root, not recursive", with(func(s *Scope) { s.Recursive = false }), "1", 0},
		{"tags any", with(func(s *Scope) { s.TagsAny = []model.Tag{"y", "z"} }), "2 3", 0},
		{"tags all", with(func(s *Scope) { s.TagsAll = []model.Tag{"x", "y"} }), "2", 0},
		{"tags both", with(func(s *Scope) { s.TagsAny = []model.Tag{"x"}; s.TagsAll = []model.Tag{"y"} }), "2", 0},
		// A snapshot: an ID filtered out is not missing; one the load lacks is.
		{"ids", with(func(s *Scope) { s.IDs = []model.ID{3, 5, 99, 98}; s.Readiness = ops.PickOpen }), "3", 2},
		{"no ids", with(func(s *Scope) { s.IDs = []model.ID{} }), "", 0},
	} {
		got, missing := l.candidates(tc.s)
		var ids []string
		for _, v := range got {
			ids = append(ids, strings.Split(key(v), "@")[0])
		}
		if strings.Join(ids, " ") != tc.want || missing != tc.missing {
			t.Errorf("%s: got %v, %d missing; want %s, %d", tc.name, ids, missing, tc.want, tc.missing)
		}
	}
}

func TestCheckFolder(t *testing.T) {
	l := &Load{Folders: []model.FolderPath{"/", "/a", "/a/b"}}
	for f, want := range map[model.FolderPath]string{"/": "", "/a/b": "", "/x": "/x", "/a/x/y": "/a/x", "/a/b/c": "/a/b/c"} {
		e := l.checkFolder(f)
		switch {
		case want == "" && e != nil:
			t.Errorf("%s: %v", f, e)
		case want != "" && (e == nil || e.Kind != errs.KindNotFound || !slices.Equal(e.Details.(errs.NotFoundDetails).Folders, []string{want})):
			t.Errorf("%s: %v, want not-found %s", f, e, want)
		}
	}
}

func TestRenderLines(t *testing.T) {
	vs := views(
		tv{id: 42, folder: "/trips/japan", r: model.Ready, priority: p(2), tags: []model.Tag{"travel"}, title: "Book flights"},
		tv{id: 43, folder: "/trips/japan", r: model.Blocked, blocking: []model.ID{42}, tags: []model.Tag{"travel"}, title: "Book hotel"},
		tv{id: 9, folder: "/trips", r: model.Complete, done: "2026-09-01T00:00:00Z", title: "Renew passport"},
	)
	want := []string{
		"42@/trips/japan\t●  42  p2   /trips/japan \t#travel \tBook flights",
		"43@/trips/japan\t◐  43  →42  /trips/japan \t#travel \tBook hotel",
		"9@/trips\t✓   9       /trips       \t        \tRenew passport",
	}
	if got := renderLines(vs, false); !slices.Equal(got, want) {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}

	// Long detail and folder are cut at 24 cells; tags and titles never are.
	long := views(
		tv{id: 1, folder: "/" + strings.Repeat("abcdefgh/", 4) + "end", r: model.Blocked, blocking: []model.ID{1000001, 1000002, 1000003, 1000004}, tags: []model.Tag{"a-very-long-tag-name", "another"}, title: strings.Repeat("t", 150)},
		// A title, wide characters and all, is never padded or cut.
		tv{id: 2, folder: "/", r: model.Ready, title: "日本へ行く"},
	)
	got := renderLines(long, false)
	if want := "1@/abcdefgh/abcdefgh/abcdefgh/abcdefgh/end\t◐  1  →1000001,1000002,100000…  /abcdefgh/abcdefgh/abcd… \t#a-very-long-tag-name #another \t" + strings.Repeat("t", 150); got[0] != want {
		t.Errorf("got\n%q\nwant\n%q", got[0], want)
	}
	if want := "2@/\t●  2                            /                        \t" + strings.Repeat(" ", 31) + "\t日本へ行く"; got[1] != want {
		t.Errorf("got\n%q\nwant\n%q", got[1], want)
	}

	// In color, folder and tags are muted, and lines not ready are dimmed.
	colored := renderLines(vs, true)
	if want := "42@/trips/japan\t●  42  p2   \x1b[90m/trips/japan\x1b[39m \t\x1b[90m#travel\x1b[39m \tBook flights"; colored[0] != want {
		t.Errorf("ready: got %q", colored[0])
	}
	if want := "43@/trips/japan\t\x1b[2m◐  43  →42  \x1b[90m/trips/japan\x1b[39m \x1b[22m\t\x1b[2m\x1b[90m#travel\x1b[39m \x1b[22m\t\x1b[2mBook hotel\x1b[22m"; colored[1] != want {
		t.Errorf("blocked: got %q", colored[1])
	}
	for i := range vs {
		if plain := strings.NewReplacer("\x1b[2m", "", "\x1b[22m", "", "\x1b[90m", "", "\x1b[39m", "").Replace(colored[i]); plain != want[i] {
			t.Errorf("colored line %d without its color: %q", i, plain)
		}
	}
}

func TestNoColor(t *testing.T) {
	for env, want := range map[string]bool{"NO_COLOR=1": true, "NO_COLOR=": false, "HOME=/h": false} {
		if got := noColor([]string{env}); got != want {
			t.Errorf("%s: %v", env, got)
		}
	}
}
