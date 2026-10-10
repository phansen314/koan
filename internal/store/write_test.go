package store

import (
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/fsys"
)

// noTemps fails the test if any temp file is left in dir, under home.
func (f *fixture) noTemps(dir string) {
	f.t.Helper()
	entries, err := os.ReadDir(f.path(dir))
	must(f.t, err)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), fsys.TempPrefix) {
			f.t.Errorf("temp file %s left in %s", e.Name(), dir)
		}
	}
}

func TestCreate(t *testing.T) {
	f := newFixture(t)
	f.mkdir("tasks/proj")
	f.write("tasks/proj/2.json", "old")
	writeTx(t, f.env, nil, func(tx *Tx) {
		wantNoErr(t, tx.Create("proj/1.json", []byte("new")))
		wantErr(t, tx.Create("proj/2.json", []byte("new")), errs.KindCorrupt,
			errs.CorruptDetails{Path: tx.Path("proj/2.json"), Reason: errs.CorruptUnexpectedFile})
		wantErr(t, tx.Create("missing/1.json", []byte("new")), errs.KindIO,
			errs.IODetails{Path: tx.Path("missing/1.json"), Code: "ENOENT"})
	})
	if got := f.read("tasks/proj/1.json"); got != "new" {
		t.Errorf("1.json is %q", got)
	}
	if got := f.read("tasks/proj/2.json"); got != "old" {
		t.Errorf("2.json is %q: clobbered", got)
	}
	f.noTemps("tasks/proj")
}

func TestReplace(t *testing.T) {
	f := newFixture(t)
	f.write("tasks/1.md", "old")
	writeTx(t, f.env, nil, func(tx *Tx) {
		wantNoErr(t, tx.Replace("1.md", []byte("new")))
		wantNoErr(t, tx.Replace("2.md", []byte("fresh")))
		if err := tx.ReplaceRaw("missing/3.md", nil); !isErrno(err, syscall.ENOENT) {
			t.Errorf("ReplaceRaw: %v", err)
		}
	})
	if got := f.read("tasks/1.md"); got != "new" {
		t.Errorf("1.md is %q", got)
	}
	if got := f.read("tasks/2.md"); got != "fresh" {
		t.Errorf("2.md is %q", got)
	}
	f.noTemps("tasks")
}

// A failure at any step leaves no temp file behind and the target untouched.
// (A failed directory flush is not a failure: see TestPublishSyncDirFails.)
func TestPublishFailures(t *testing.T) {
	for _, tc := range []struct {
		op   string
		code string
	}{
		{fsys.OpCreateTemp, "EACCES"},
		{fsys.OpWrite, "ENOSPC"},
		{fsys.OpSyncFile, "EIO"},
		{fsys.OpCloseFile, "EIO"},
		{fsys.OpRename, "EXDEV"},
		{fsys.OpLink, "EMLINK"},
	} {
		t.Run(tc.op, func(t *testing.T) {
			f := newFixture(t)
			f.write("tasks/1.json", "old")
			errno := map[string]syscall.Errno{"EACCES": syscall.EACCES, "ENOSPC": syscall.ENOSPC, "EIO": syscall.EIO, "EXDEV": syscall.EXDEV, "EMLINK": syscall.EMLINK}[tc.code]
			env := f.withFault(fsys.ErrnoAt(tc.op, "", 1, errno))
			writeTx(t, env, nil, func(tx *Tx) {
				var e *errs.Error
				if tc.op == fsys.OpLink {
					e = tx.Create("2.json", []byte("new"))
				} else {
					e = tx.Replace("1.json", []byte("new"))
				}
				rel := map[bool]string{true: "2.json", false: "1.json"}[tc.op == fsys.OpLink]
				wantErr(t, e, errs.KindIO, errs.IODetails{Path: tx.Path(rel), Code: tc.code})
			})
			if got := f.read("tasks/1.json"); got != "old" {
				t.Errorf("1.json is %q", got)
			}
			if _, err := os.Lstat(f.root + "/2.json"); err == nil {
				t.Error("2.json created")
			}
			f.noTemps("tasks")
		})
	}
}

// Publish's temp file is created in the target's directory, with koan's
// prefix, and published by link or rename as asked.
func TestPublishSteps(t *testing.T) {
	f := newFixture(t)
	f.mkdir("tasks/proj")
	var ops []fsys.Op
	writeTx(t, f.withFault(fsys.Record(&ops)), nil, func(tx *Tx) {
		ops = nil
		wantNoErr(t, tx.Create("proj/1.json", []byte("x")))
		wantNoErr(t, tx.Replace("proj/1.json", []byte("y")))
	})
	var names []string
	for _, op := range ops {
		if op.Mutating || op.Name == fsys.OpSyncFile || op.Name == fsys.OpSyncDir {
			names = append(names, op.Name)
		}
		if op.Name == fsys.OpSyncDir && op.Path != "proj" {
			t.Errorf("synced %q", op.Path)
		}
		if op.Name == fsys.OpCreateTemp && op.Path != "proj" {
			t.Errorf("temp file created in %q", op.Path)
		}
		if (op.Name == fsys.OpLink || op.Name == fsys.OpRename) && !strings.HasPrefix(op.Path, "proj/"+fsys.TempPrefix) {
			t.Errorf("%s from %q", op.Name, op.Path)
		}
	}
	want := "createtemp write syncfile link syncdir remove createtemp write syncfile rename syncdir"
	if got := strings.Join(names, " "); got != want {
		t.Errorf("steps %q, want %q", got, want)
	}
}

// A directory that can't be flushed doesn't fail the write: the file is
// already published, so reporting an error would say it wasn't.
func TestPublishSyncDirFails(t *testing.T) {
	f := newFixture(t)
	env := f.withFault(fsys.ErrnoAt(fsys.OpSyncDir, "", 1, syscall.EIO))
	writeTx(t, env, nil, func(tx *Tx) {
		wantNoErr(t, tx.Create("2.json", []byte("new")))
	})
	if got := f.read("tasks/2.json"); got != "new" {
		t.Errorf("2.json is %q", got)
	}
	f.noTemps("tasks")
}

func TestWritesNeedWrite(t *testing.T) {
	f := newFixture(t)
	readTx(t, f.env, nil, func(tx *Tx) {
		wantKind(t, tx.Create("1.json", nil), errs.KindInternal)
		wantKind(t, tx.Replace("1.json", nil), errs.KindInternal)
		wantKind(t, tx.SetLastID(5), errs.KindInternal)
		if tx.ReplaceRaw("1.md", nil) == nil || tx.Mkdir("proj") == nil {
			t.Error("a write call succeeded in a read")
		}
	})
	if _, err := os.Lstat(f.root + "/1.json"); err == nil {
		t.Error("1.json written in a read")
	}
}

func TestSetLastID(t *testing.T) {
	f := newFixture(t)
	koanJSON := f.read("tasks/" + MetaName)
	f.write("cfg/.koan-tmp-stale", "x") // an interrupted write's leftover
	writeTx(t, f.env, nil, func(tx *Tx) {
		wantNoErr(t, tx.SetLastID(101))
		if got := tx.LastID(); got != 101 {
			t.Errorf("LastID = %d", got)
		}
	})
	if got, want := f.read("cfg/"+StateName), stateJSON(f.root, 101); got != want {
		t.Errorf("state file is %q, want %q", got, want)
	}
	if f.read("tasks/"+MetaName) != koanJSON {
		t.Error("an ID-issuing write changed koan.json")
	}
	if _, err := os.Lstat(f.path("cfg/.koan-tmp-stale")); !os.IsNotExist(err) {
		t.Error("the stale temp file in the config directory is still there")
	}
	f.noTemps("cfg")
	env := f.withFault(fsys.ErrnoAt(fsys.OpRename, "", 1, syscall.EROFS))
	writeTx(t, env, nil, func(tx *Tx) {
		wantErr(t, tx.SetLastID(102), errs.KindIO, errs.IODetails{Path: f.path("cfg/" + StateName), Code: "EROFS"})
		if got := tx.LastID(); got != 101 {
			t.Errorf("LastID = %d after a failed write", got)
		}
	})
	if got := f.read("cfg/" + StateName); got != stateJSON(f.root, 101) {
		t.Errorf("state file after a failed write: %q", got)
	}
}

// CreateState writes a state file naming the root, replacing any there.
func TestCreateState(t *testing.T) {
	f := newFixture(t)
	f.write("cfg/"+StateName, stateJSON("/elsewhere", 9))
	diagnoseTx(t, f.env, func(tx *Tx) {
		if st, e := tx.StateState(); st != StateOtherRoot || e == nil || e.Kind != errs.KindNotInitialized {
			t.Errorf("StateState = %v %v", st, e)
		}
		wantNoErr(t, tx.CreateState(42))
		if st, e := tx.StateState(); st != StateOK || e != nil || tx.LastID() != 42 {
			t.Errorf("after CreateState: %v %v %d", st, e, tx.LastID())
		}
	})
	if got := f.read("cfg/" + StateName); got != stateJSON(f.root, 42) {
		t.Errorf("state file %q", got)
	}
}

func TestMkdir(t *testing.T) {
	f := newFixture(t)
	writeTx(t, f.env, nil, func(tx *Tx) {
		must(t, tx.Mkdir("proj"))
		if err := tx.Mkdir("proj"); !isErrno(err, syscall.EEXIST) {
			t.Errorf("second Mkdir: %v", err)
		}
	})
	fi, err := os.Lstat(f.root + "/proj")
	must(t, err)
	if !fi.IsDir() {
		t.Error("not a directory")
	}
}

func TestNotesMissing(t *testing.T) {
	f := newFixture(t)
	var w errs.Collector
	writeTx(t, f.env, &w, func(tx *Tx) {
		err := tx.ReplaceRaw("missing/1.md", []byte("notes"))
		tx.NotesMissing("missing/1.md", 1, err)
		tx.NotesMissing("x.md", 2, errs.Internal("no errno")) // no symbolic name: no code
	})
	ws := w.Warnings()
	if len(ws) != 2 || ws[0].Kind != errs.WarnNotesMissing || ws[0].Paths[0] != f.root+"/missing/1.md" || ws[0].Code != "ENOENT" ||
		ws[1].Kind != errs.WarnNotesMissing || ws[1].Paths[0] != f.root+"/x.md" || ws[1].Code != "" {
		t.Errorf("warnings %+v", ws)
	}
}
