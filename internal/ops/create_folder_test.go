package ops

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/schematest"
)

// createFolder runs create-folder with input over f, checking the envelope,
// and create-folder-output on success or create-folder-partial when there is
// a partial; it returns the result or error in short, the home as "~".
func (f *fixture) createFolder(input string) string {
	f.t.Helper()
	e := Run("create-folder", parse(f.t, input), nil, f.env)
	line(f.t, e)
	enc := func(schema string, v any) string {
		b, err := jsonio.MarshalLine(v)
		if err != nil {
			f.t.Fatal(err)
		}
		if schema != "" {
			if ok, fl := schematest.Check(f.t, schema, b); !ok {
				f.t.Errorf("%s rejects at %s: %s", schema, fl, b)
			}
		}
		return strings.TrimSpace(string(b))
	}
	if e.OK {
		return enc("create-folder-output", e.Result)
	}
	s := string(e.Error.Kind) + " " + enc("", e.Error.Details)
	if e.Error.Partial != nil {
		s += " partial " + enc("create-folder-partial", e.Error.Partial)
	}
	return f.rel(s)
}

// isDir reports whether rel, under the home, is a plain directory.
func (f *fixture) isDir(rel string) bool {
	fi, err := os.Lstat(filepath.Join(f.home, rel))
	return err == nil && fi.IsDir()
}

func TestCreateFolder(t *testing.T) {
	symlink := func(f *fixture) {
		f.mkdir("real")
		if err := os.Symlink(filepath.Join(f.home, "real"), filepath.Join(f.root, "a")); err != nil {
			f.t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name   string
		setup  func(f *fixture)
		input  string
		want   string
		dirs   []string // plain directories afterwards, under the root
		absent []string // nothing there afterwards, under the root
	}{
		// State of the path (operations.md, create-folder, Preconditions).
		{"root", nil, `{"folder": "/"}`, `{"folder":"/","created":[]}`, nil, nil},
		{"exists", func(f *fixture) { f.mkdir("tasks/a/b") }, `{"folder": "/a/b"}`, `{"folder":"/a/b","created":[]}`, nil, nil},
		{"exists, with parents", func(f *fixture) { f.mkdir("tasks/a/b") }, `{"folder": "/a/b", "parents": true}`, `{"folder":"/a/b","created":[]}`, nil, nil},
		{"folder itself missing", func(f *fixture) { f.mkdir("tasks/a") }, `{"folder": "/a/b"}`, `{"folder":"/a/b","created":["/a/b"]}`, []string{"a/b"}, nil},
		{"top-level folder", nil, `{"folder": "/a"}`, `{"folder":"/a","created":["/a"]}`, []string{"a"}, nil},
		{"parent missing", nil, `{"folder": "/a/b/c"}`, `not-found {"folders":["/a"],"ids":[],"paths":[]}`, nil, []string{"a"}},
		{"inner parent missing", func(f *fixture) { f.mkdir("tasks/a") }, `{"folder": "/a/b/c"}`, `not-found {"folders":["/a/b"],"ids":[],"paths":[]}`, nil, []string{"a/b"}},
		{"parents created", func(f *fixture) { f.mkdir("tasks/a") }, `{"folder": "/a/b/c/d", "parents": true}`,
			`{"folder":"/a/b/c/d","created":["/a/b","/a/b/c","/a/b/c/d"]}`, []string{"a/b/c/d"}, nil},
		{"parent a file", func(f *fixture) { f.write("tasks/a", "") }, `{"folder": "/a/b", "parents": true}`,
			`corrupt {"path":"~/tasks/a","reason":"unexpected-file"}`, nil, nil},
		{"folder itself a file", func(f *fixture) { f.write("tasks/a", "") }, `{"folder": "/a"}`,
			`corrupt {"path":"~/tasks/a","reason":"unexpected-file"}`, nil, nil},
		{"parent a symlink: never followed", symlink, `{"folder": "/a/b", "parents": true}`,
			`corrupt {"path":"~/tasks/a","reason":"unexpected-file"}`, nil, []string{"../real/b"}},
		{"folder itself a symlink", symlink, `{"folder": "/a"}`,
			`corrupt {"path":"~/tasks/a","reason":"unexpected-file"}`, nil, nil},

		// Case is kept, and siblings must differ by more than case.
		{"mixed case", func(f *fixture) { f.mkdir("tasks/work") }, `{"folder": "/Work-2/API", "parents": true}`,
			`{"folder":"/Work-2/API","created":["/Work-2","/Work-2/API"]}`, []string{"Work-2/API"}, nil},
		{"differs only in case", func(f *fixture) { f.mkdir("tasks/work") }, `{"folder": "/Work"}`,
			`conflict {"rule":"case-clash","ids":[]}`, nil, []string{"Work"}},
		{"a parent differs only in case", func(f *fixture) { f.mkdir("tasks/Work") }, `{"folder": "/work/a", "parents": true}`,
			`conflict {"rule":"case-clash","ids":[]}`, nil, []string{"work"}},
		{"a file differs only in case", func(f *fixture) { f.write("tasks/Work", "") }, `{"folder": "/work"}`,
			`conflict {"rule":"case-clash","ids":[]}`, nil, []string{"work"}},
		{"exact case exists beside a variant", func(f *fixture) { f.mkdir("tasks/Work"); f.mkdir("tasks/work") }, `{"folder": "/work/a"}`,
			`{"folder":"/work/a","created":["/work/a"]}`, []string{"work/a"}, nil},

		// Root states come first.
		{"corrupt koan.json", func(f *fixture) { f.write("tasks/koan.json", "{") }, `{"folder": "/a"}`,
			`corrupt {"path":"~/tasks/koan.json","reason":"not-json","detail":"not valid JSON: unexpected end of input"}`, nil, nil},

		// Failures midway: what was created stays, and is the partial.
		{"first mkdir fails", func(f *fixture) { f.fail(fsys.OpMkdir, "a", syscall.EACCES) }, `{"folder": "/a/b", "parents": true}`,
			`io {"path":"~/tasks/a","code":"EACCES"}`, nil, []string{"a"}},
		{"mkdir fails midway", func(f *fixture) { f.fail(fsys.OpMkdir, "a/b/c", syscall.ENOSPC) }, `{"folder": "/a/b/c", "parents": true}`,
			`io {"path":"~/tasks/a/b/c","code":"ENOSPC"} partial {"created":["/a","/a/b"]}`, []string{"a/b"}, []string{"a/b/c"}},

		// Something appears between the walk and mkdir (an outside change).
		{"a directory appears", func(f *fixture) { f.appearAt("a/b", f.mkdir) }, `{"folder": "/a/b/c", "parents": true}`,
			`{"folder":"/a/b/c","created":["/a","/a/b/c"]}`, []string{"a/b/c"}, nil},
		{"a case variant appears", func(f *fixture) { f.appearAt("a", func(rel string) { f.mkdir(rel + "/B") }) }, `{"folder": "/a/b/c", "parents": true}`,
			`conflict {"rule":"case-clash","ids":[]}`, []string{"a/B"}, []string{"a/b"}},
		{"a file appears", func(f *fixture) { f.appearAt("a/b", func(rel string) { f.write(rel, "") }) }, `{"folder": "/a/b/c", "parents": true}`,
			`corrupt {"path":"~/tasks/a/b","reason":"unexpected-file"} partial {"created":["/a"]}`, []string{"a"}, []string{"a/b/c"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			if tc.setup != nil {
				tc.setup(f)
			}
			if got := f.createFolder(tc.input); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
			for _, d := range tc.dirs {
				if !f.isDir(filepath.Join("tasks", d)) {
					t.Errorf("%s is not a directory", d)
				}
			}
			for _, d := range tc.absent {
				if _, err := os.Lstat(filepath.Join(f.root, d)); err == nil {
					t.Errorf("%s was created", d)
				}
			}
		})
	}
}

// appearAt makes something appear at rel, under the root, just before
// create-folder's mkdir of it, by calling make with rel under the home.
func (f *fixture) appearAt(rel string, make func(rel string)) {
	f.hook(func(o fsys.Op) error {
		if o.Name == fsys.OpMkdir && o.Path == rel {
			make(filepath.Join("tasks", rel))
		}
		return nil
	})
}

// After a partial, rerunning completes the chain.
func TestCreateFolderRetry(t *testing.T) {
	f := newFixture(t)
	f.fail(fsys.OpMkdir, "a/b", syscall.ENOSPC)
	if got := f.createFolder(`{"folder": "/a/b/c", "parents": true}`); !strings.Contains(got, `partial {"created":["/a"]}`) {
		t.Fatalf("first run: %s", got)
	}
	f.env.FS = fsys.OS{}
	if got := f.createFolder(`{"folder": "/a/b/c", "parents": true}`); got != `{"folder":"/a/b/c","created":["/a/b","/a/b/c"]}` {
		t.Errorf("retry: %s", got)
	}
}

// Another write holding the lock: busy, and nothing created.
func TestCreateFolderBusy(t *testing.T) {
	f := newFixture(t)
	r, err := fsys.OS{}.OpenRoot(f.root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	l, err := r.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Unlock()
	if got := f.createFolder(`{"folder": "/a"}`); got != "busy {}" {
		t.Errorf("got %s", got)
	}
	if f.isDir("tasks/a") {
		t.Error("folder created while busy")
	}
}
