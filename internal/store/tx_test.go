package store

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/migrations"
)

func TestReadUsableRoot(t *testing.T) {
	f := newFixture(t)
	readTx(t, f.env, nil, func(tx *Tx) {
		if got := tx.LastID(); got != 100 {
			t.Errorf("last_id %d", got)
		}
		if got := tx.Path("proj/1.json"); got != f.root+"/proj/1.json" {
			t.Errorf("Path: %q", got)
		}
		if got := tx.Path("."); got != f.root {
			t.Errorf("Path(.): %q", got)
		}
	})
}

// A "~/" root is expanded into the home directory, in reported paths too.
func TestReadHomeRoot(t *testing.T) {
	f := newFixture(t)
	f.write("cfg/"+ConfigName, "root = \"~/tasks/\"\n")
	readTx(t, f.env, nil, func(tx *Tx) {
		if got := tx.Path("1.json"); got != f.root+"/1.json" {
			t.Errorf("Path: %q", got)
		}
	})
}

func TestJoinPath(t *testing.T) {
	for _, tc := range [][3]string{
		{"/r", ".", "/r"}, {"/r", "a/b", "/r/a/b"}, {"/", "a", "/a"}, {"/", ".", "/"},
	} {
		if got := joinPath(tc[0], tc[1]); got != tc[2] {
			t.Errorf("joinPath(%q, %q) = %q, want %q", tc[0], tc[1], got, tc[2])
		}
	}
}

// Each Root states error, in the order the checks run (operations.md,
// Precedence step 2).
func TestRootStates(t *testing.T) {
	cfgPath := func(f *fixture) string { return f.path("cfg/" + ConfigName) }
	meta := func(f *fixture) string { return f.root + "/" + MetaName }
	for _, tc := range []struct {
		name  string
		setup func(f *fixture)
		kind  errs.Kind
		want  func(f *fixture) any
	}{
		{"no config directory", func(f *fixture) { f.env.ConfigDir = "" },
			errs.KindEnvironment, func(*fixture) any { return errs.EnvironmentDetails{Variable: "HOME"} }},
		{"missing config", func(f *fixture) { must(t, os.Remove(cfgPath(f))) },
			errs.KindNotInitialized, func(*fixture) any { return errs.NotInitializedDetails{Missing: errs.MissingConfig} }},
		{"config through a dangling symlink", func(f *fixture) {
			must(t, os.Remove(cfgPath(f)))
			must(t, os.Symlink("nowhere", cfgPath(f)))
		}, errs.KindNotInitialized, func(*fixture) any { return errs.NotInitializedDetails{Missing: errs.MissingConfig} }},
		{"unreadable config", func(f *fixture) {
			f.env = f.withFault(func(o fsys.Op) error {
				if o.Name == fsys.OpReadFile && o.Path == cfgPath(f) {
					return syscall.EACCES
				}
				return nil
			})
		},
			errs.KindIO, func(f *fixture) any { return errs.IODetails{Path: cfgPath(f), Code: "EACCES"} }},
		{"config is a directory", func(f *fixture) {
			must(t, os.Remove(cfgPath(f)))
			must(t, os.Mkdir(cfgPath(f), 0o755))
		}, errs.KindCorrupt, func(f *fixture) any { return errs.CorruptDetails{Path: cfgPath(f), Reason: errs.CorruptUnexpectedFile} }},
		{"config is a FIFO", func(f *fixture) {
			must(t, os.Remove(cfgPath(f)))
			must(t, syscall.Mkfifo(cfgPath(f), 0o644))
		}, errs.KindCorrupt, func(f *fixture) any { return errs.CorruptDetails{Path: cfgPath(f), Reason: errs.CorruptUnexpectedFile} }},
		{"corrupt config", func(f *fixture) { f.write("cfg/"+ConfigName, "root = /x\n") },
			errs.KindCorrupt, func(f *fixture) any {
				return errs.CorruptDetails{Path: cfgPath(f), Reason: errs.CorruptInvalid, Detail: "line 1: root must be a double-quoted string"}
			}},
		{"illegal root form", func(f *fixture) { f.write("cfg/"+ConfigName, "root = \"tasks\"\n") },
			errs.KindCorrupt, func(f *fixture) any {
				return errs.CorruptDetails{Path: cfgPath(f), Reason: errs.CorruptInvalid, Detail: "root must be an absolute path or begin with ~/"}
			}},
		{"root with ..", func(f *fixture) { f.write("cfg/"+ConfigName, "root = \"/a/../tasks\"\n") },
			errs.KindCorrupt, func(f *fixture) any {
				return errs.CorruptDetails{Path: cfgPath(f), Reason: errs.CorruptInvalid, Detail: "root must not contain a .. segment"}
			}},
		{"root with NUL", func(f *fixture) { f.write("cfg/"+ConfigName, "root = \"/tmp/x\\u0000y\"\n") },
			errs.KindCorrupt, func(f *fixture) any {
				return errs.CorruptDetails{Path: cfgPath(f), Reason: errs.CorruptInvalid, Detail: "root must not contain a NUL character"}
			}},
		{"~/ root without home", func(f *fixture) {
			f.write("cfg/"+ConfigName, "root = \"~/tasks\"\n")
			f.env.Home = ""
		}, errs.KindNotInitialized, func(*fixture) any { return errs.NotInitializedDetails{Missing: errs.MissingRoot} }},
		{"missing root", func(f *fixture) { must(t, os.RemoveAll(f.root)) },
			errs.KindNotInitialized, func(*fixture) any { return errs.NotInitializedDetails{Missing: errs.MissingRoot} }},
		{"root is a file", func(f *fixture) {
			must(t, os.RemoveAll(f.root))
			f.write("tasks", "")
		}, errs.KindNotInitialized, func(*fixture) any { return errs.NotInitializedDetails{Missing: errs.MissingRoot} }},
		{"root is a symlink loop", func(f *fixture) {
			must(t, os.RemoveAll(f.root))
			must(t, os.Symlink("tasks", f.root))
		}, errs.KindNotInitialized, func(*fixture) any { return errs.NotInitializedDetails{Missing: errs.MissingRoot} }},
		{"missing koan.json", func(f *fixture) { must(t, os.Remove(meta(f))) },
			errs.KindNotInitialized, func(*fixture) any { return errs.NotInitializedDetails{Missing: errs.MissingMetadata} }},
		{"koan.json is a symlink", func(f *fixture) {
			must(t, os.Rename(meta(f), f.root+"/real"))
			must(t, os.Symlink("real", meta(f)))
		}, errs.KindCorrupt, func(f *fixture) any { return errs.CorruptDetails{Path: meta(f), Reason: errs.CorruptUnexpectedFile} }},
		{"koan.json is a directory", func(f *fixture) {
			must(t, os.Remove(meta(f)))
			must(t, os.Mkdir(meta(f), 0o755))
		}, errs.KindCorrupt, func(f *fixture) any { return errs.CorruptDetails{Path: meta(f), Reason: errs.CorruptUnexpectedFile} }},
		{"koan.json is a FIFO", func(f *fixture) {
			must(t, os.Remove(meta(f)))
			must(t, syscall.Mkfifo(meta(f), 0o644))
		}, errs.KindCorrupt, func(f *fixture) any { return errs.CorruptDetails{Path: meta(f), Reason: errs.CorruptUnexpectedFile} }},
		{"koan.json not JSON", func(f *fixture) { f.write("tasks/"+MetaName, "{") },
			errs.KindCorrupt, func(f *fixture) any {
				return errs.CorruptDetails{Path: meta(f), Reason: errs.CorruptNotJSON, Detail: "not valid JSON: unexpected end of input"}
			}},
		{"koan.json empty", func(f *fixture) { f.write("tasks/"+MetaName, "") },
			errs.KindCorrupt, func(f *fixture) any {
				return errs.CorruptDetails{Path: meta(f), Reason: errs.CorruptNotJSON, Detail: "empty"}
			}},
		{"koan.json invalid", func(f *fixture) { f.write("tasks/"+MetaName, `{"schema": 2, "migration": -1}`) },
			errs.KindCorrupt, func(f *fixture) any {
				return errs.CorruptDetails{Path: meta(f), Reason: errs.CorruptInvalid,
					Problems: []errs.Problem{{Field: "/migration", Reason: "must be between 0 and 9007199254740991"}}}
			}},
		{"koan.json unsupported", func(f *fixture) { f.write("tasks/"+MetaName, `{"schema": 3, "whatever": true}`) },
			errs.KindUnsupportedFormat, func(f *fixture) any {
				return errs.UnsupportedFormatDetails{Path: meta(f), Found: 3, Supported: []int64{2}}
			}},
		{"koan.json schema 0", func(f *fixture) { f.write("tasks/"+MetaName, `{"schema": 0, "last_id": 1}`) },
			errs.KindUnsupportedFormat, func(f *fixture) any {
				return errs.UnsupportedFormatDetails{Path: meta(f), Found: 0, Supported: []int64{2}}
			}},
		{"koan.json past the latest step", func(f *fixture) { f.write("tasks/"+MetaName, `{"schema": 2, "migration": 2}`) },
			errs.KindUnsupportedFormat, func(f *fixture) any {
				return errs.UnsupportedFormatDetails{Path: meta(f), Found: 2, Supported: []int64{1}, Field: "migration"}
			}},
		{"koan.json in an older format", func(f *fixture) { f.write("tasks/"+MetaName, `{"schema": 1, "last_id": 1}`) },
			errs.KindMigrationPending, func(*fixture) any { return errs.MigrationPendingDetails{Recorded: 0, Latest: 1} }},
		{"koan.json a step behind", func(f *fixture) { f.write("tasks/"+MetaName, `{"schema": 2, "migration": 0}`) },
			errs.KindMigrationPending, func(*fixture) any { return errs.MigrationPendingDetails{Recorded: 0, Latest: 1} }},
		{"older koan.json breaking its own format", func(f *fixture) { f.write("tasks/"+MetaName, `{"schema": 1, "last_id": -1}`) },
			errs.KindCorrupt, func(f *fixture) any {
				return errs.CorruptDetails{Path: meta(f), Reason: errs.CorruptInvalid,
					Problems: []errs.Problem{{Field: "/last_id", Reason: "must be between 0 and 999999999999999"}}}
			}},
		{"older koan.json with the counter", func(f *fixture) { f.write("tasks/"+MetaName, `{"schema": 1, "last_id": 1, "migration": 0}`) },
			errs.KindCorrupt, func(f *fixture) any {
				return errs.CorruptDetails{Path: meta(f), Reason: errs.CorruptInvalid,
					Problems: []errs.Problem{{Field: "/migration", Reason: "unknown field"}}}
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.setup(f)
			ran := false
			e := Read(f.env, nil, func(*Tx) *errs.Error { ran = true; return nil })
			wantErr(t, e, tc.kind, tc.want(f))
			if ran {
				t.Error("fn ran")
			}
			e = Write(f.env, nil, func(*Tx) *errs.Error { ran = true; return nil })
			wantErr(t, e, tc.kind, tc.want(f))
			if ran {
				t.Error("fn ran")
			}
		})
	}
}

func TestRootUnreadable(t *testing.T) {
	f := newFixture(t)
	env := f.withFault(fsys.ErrnoAt(fsys.OpOpenRoot, "", 1, syscall.EACCES))
	wantErr(t, Read(env, nil, nil), errs.KindIO, errs.IODetails{Path: f.root, Code: "EACCES"})
	env = f.withFault(fsys.ErrnoAt(fsys.OpReadFile, MetaName, 1, syscall.EIO))
	wantErr(t, Read(env, nil, nil), errs.KindIO, errs.IODetails{Path: f.root + "/" + MetaName, Code: "EIO"})
}

func TestWriteBusy(t *testing.T) {
	f := newFixture(t)
	r, err := fsys.OS{}.OpenRoot(f.root)
	must(t, err)
	defer r.Close()
	l, err := r.Lock()
	must(t, err)
	defer l.Unlock()

	wantKind(t, Write(f.env, nil, nil), errs.KindBusy)
	// Reads take no lock.
	readTx(t, f.env, nil, func(*Tx) {})
	// Root states come before busy.
	f.write("tasks/"+MetaName, "{")
	wantErr(t, Write(f.env, nil, nil), errs.KindCorrupt,
		errs.CorruptDetails{Path: f.root + "/" + MetaName, Reason: errs.CorruptNotJSON, Detail: "not valid JSON: unexpected end of input"})
}

// The lock is held while fn runs, and released when it returns.
func TestWriteHoldsLock(t *testing.T) {
	f := newFixture(t)
	writeTx(t, f.env, nil, func(*Tx) {
		wantKind(t, Write(f.env, nil, nil), errs.KindBusy)
	})
	writeTx(t, f.env, nil, func(*Tx) {})
}

// heldFor makes the first n Lock calls find the lock held, and counts every
// call in calls.
func heldFor(n int, calls *int) fsys.Hook {
	return func(op fsys.Op) error {
		if op.Name != fsys.OpLock {
			return nil
		}
		*calls++
		if *calls <= n {
			return syscall.EAGAIN
		}
		return nil
	}
}

// A write that finds the lock held retries until it is free, within its wait;
// so do doctor and repair.
func TestLockWait(t *testing.T) {
	f := newFixture(t)
	for name, tx := range map[string]func(Env, *errs.Collector, func(*Tx) *errs.Error) *errs.Error{"Write": Write, "Diagnose": Diagnose} {
		calls := 0
		env := f.withFault(heldFor(3, &calls))
		env.LockWait = time.Second
		if e := tx(env, nil, func(*Tx) *errs.Error { return nil }); e != nil {
			t.Errorf("%s: %v", name, e)
		}
		if calls != 4 {
			t.Errorf("%s: Lock called %d times, want 4", name, calls)
		}
	}
}

// A lock still held once the wait is over is busy.
func TestLockWaitBusy(t *testing.T) {
	f := newFixture(t)
	r, err := fsys.OS{}.OpenRoot(f.root)
	must(t, err)
	defer r.Close()
	l, err := r.Lock()
	must(t, err)
	defer l.Unlock()

	env := f.env
	env.LockWait = 50 * time.Millisecond
	start := time.Now()
	wantKind(t, Write(env, nil, nil), errs.KindBusy)
	if d := time.Since(start); d < env.LockWait || d > time.Second {
		t.Errorf("busy after %v, want the %v wait", d, env.LockWait)
	}
}

func TestWriteLockError(t *testing.T) {
	f := newFixture(t)
	env := f.withFault(fsys.ErrnoAt(fsys.OpLock, "", 1, syscall.EIO))
	wantErr(t, Write(env, nil, nil), errs.KindIO, errs.IODetails{Path: f.root, Code: "EIO"})
}

// A write re-reads koan.json and the state file once it holds the lock: a
// change made while it waited is seen, and a problem found only then is a
// Root states error.
func TestWriteRereadsMeta(t *testing.T) {
	f := newFixture(t)
	change := func(rel, content string) fsys.Hook {
		return func(op fsys.Op) error {
			if op.Name == fsys.OpLock {
				f.write(rel, content)
			}
			return nil
		}
	}
	writeTx(t, f.withFault(change("cfg/"+StateName, stateJSON(f.root, 7))), nil, func(tx *Tx) {
		if got := tx.LastID(); got != 7 {
			t.Errorf("last_id %d, want 7", got)
		}
	})
	wantErr(t, Write(f.withFault(change("tasks/"+MetaName, `{"schema": 3}`)), nil, nil), errs.KindUnsupportedFormat,
		errs.UnsupportedFormatDetails{Path: f.root + "/" + MetaName, Found: 3, Supported: []int64{2}})
	// A step made pending while it waited is migration-pending, not a write.
	f.write("tasks/"+MetaName, "{\"schema\": 2, \"migration\": 1}\n")
	ran := false
	wantErr(t, Write(f.withFault(change("tasks/"+MetaName, `{"schema": 1, "last_id": 7}`)), nil, func(*Tx) *errs.Error { ran = true; return nil }),
		errs.KindMigrationPending, errs.MigrationPendingDetails{Recorded: 0, Latest: 1})
	if ran {
		t.Error("fn ran")
	}
	// The state file, changed while it waited.
	f.write("tasks/"+MetaName, "{\"schema\": 2, \"migration\": 1}\n")
	for content, want := range map[string]*errs.Error{
		"{":                        errs.CorruptBy(f.path("cfg/"+StateName), errs.CorruptCause{Reason: errs.CorruptNotJSON, Detail: "not valid JSON: unexpected end of input"}),
		stateJSON("/elsewhere", 1): errs.NotInitialized(errs.MissingState),
		`{"schema": 2}`:            errs.UnsupportedFormat(f.path("cfg/"+StateName), 2, []int64{1}),
	} {
		f.write("cfg/"+StateName, stateJSON(f.root, 100))
		e := Write(f.withFault(change("cfg/"+StateName, content)), nil, func(*Tx) *errs.Error { t.Error("fn ran"); return nil })
		wantErr(t, e, want.Kind, want.Details)
	}
}

// The error fn returns is Read's and Write's; warnings reach the collector.
func TestTxResult(t *testing.T) {
	f := newFixture(t)
	var w errs.Collector
	want := errs.Internal("boom")
	got := Read(f.env, &w, func(tx *Tx) *errs.Error {
		tx.Warn(errs.UnreadableFolder("/x", "EACCES"))
		return want
	})
	if got != want {
		t.Errorf("got %v", got)
	}
	if ws := w.Warnings(); len(ws) != 1 || ws[0].Paths[0] != "/x" {
		t.Errorf("warnings %+v", ws)
	}
}

// The root path is resolved once: every call goes through the opened root,
// so repointing a symlinked root mid-write does not split the write.
func TestOneResolution(t *testing.T) {
	f := newFixture(t)
	other := f.path("other")
	f.mkdir("other")
	link := f.path("link")
	must(t, os.Symlink(f.root, link))
	f.write("cfg/"+ConfigName, string(EncodeConfig(link)))
	f.write("cfg/"+StateName, stateJSON(link, 100))
	writeTx(t, f.env, nil, func(tx *Tx) {
		must(t, os.Remove(link))
		must(t, os.Symlink(other, link))
		wantNoErr(t, tx.Create("1.json", []byte("x")))
	})
	if _, err := os.Stat(filepath.Join(f.root, "1.json")); err != nil {
		t.Errorf("not written to the root the write locked: %v", err)
	}
}

// MigrateFormats accepts a koan.json in an older format or behind, and keeps
// the ordered tree of an older task file (and of koan.json); the other
// transactions keep neither, and treat the file as unsupported.
func TestMigrateFormatsCache(t *testing.T) {
	step := migrations.Step{Number: 2, Name: "t", Task: &migrations.Format{From: 0}}
	t.Cleanup(migrations.UseSteps(append(migrations.Steps(), step)))
	f := newFixture(t)
	f.write("tasks/koan.json", `{"schema": 1, "last_id": 5}`)
	f.write("tasks/1.json", `{"schema": 0}`)
	loc := Location{Folder: "/", ID: 1}

	e := MigrateFormats(f.env, nil, func(tx *Tx) *errs.Error {
		obj, schema, ok := tx.OldMeta()
		if last, holds := tx.OldLastID(); !ok || schema != 1 || obj == nil || !holds || last != 5 {
			t.Errorf("OldMeta = %v %d %v", obj, schema, ok)
		}
		ld := tx.Load(loc)
		if ld.State != Unsupported || !ld.Older || ld.Tree == nil || ld.Found != 0 {
			t.Errorf("loaded %+v", ld)
		}
		return nil
	})
	wantNoErr(t, e)

	f.write("tasks/koan.json", `{"schema": 2, "migration": 1}`)
	wantNoErr(t, Diagnose(f.env, nil, func(tx *Tx) *errs.Error {
		if ld := tx.Load(loc); ld.State != Unsupported || !ld.Older || ld.Tree != nil {
			t.Errorf("loaded %+v", ld)
		}
		return nil
	}))

	// Its own errors, after busy.
	for content, kind := range map[string]errs.Kind{
		"{": errs.KindCorrupt, `{"schema": 3}`: errs.KindUnsupportedFormat, `{"schema": 2, "migration": 9}`: errs.KindUnsupportedFormat,
		`{"schema": 1, "last_id": -1}`: errs.KindCorrupt,
	} {
		f.write("tasks/koan.json", content)
		wantKind(t, MigrateFormats(f.env, nil, func(*Tx) *errs.Error { t.Error("fn ran"); return nil }), kind)
	}
	must(t, os.Remove(f.path("tasks/koan.json")))
	wantKind(t, MigrateFormats(f.env, nil, nil), errs.KindNotInitialized)
}
