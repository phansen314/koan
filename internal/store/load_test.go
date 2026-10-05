package store

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/fsys"
	"github.com/phansen314/ftask/internal/model"
)

func TestLoad(t *testing.T) {
	f := newFixture(t)
	f.task("proj", 1, false, 2)
	f.write("tasks/proj/2.json", "{")
	f.write("tasks/proj/3.json", "")
	f.task("proj", 4, false)
	f.write("tasks/proj/5.json", `{"schema": 2}`)
	f.task("proj", 7, false)
	f.write("tasks/proj/4.json", f.read("tasks/proj/7.json")) // id 7 in 4.json

	env := f.withFault(fsys.ErrnoAt(fsys.OpReadFile, "proj/6.json", 1, syscall.EIO))
	f.task("proj", 6, false)
	var w errs.Collector
	readTx(t, env, &w, func(tx *Tx) {
		ld := tx.Load(Location{"/proj", 1})
		if ld.State != Usable || ld.Task.ID != 1 || ld.Task.BlockedBy[0] != 2 {
			t.Errorf("1: %+v", ld)
		}
		wantNoErr(t, tx.Needed(ld))
		wantNoErr(t, tx.Relevant(ld))
		task := tx.Task(ld)
		if task.Folder != "/proj" || task.NotesPath != f.root+"/proj/1.md" || task.Title != "task 1" {
			t.Errorf("Task: %+v", task)
		}

		for _, tc := range []struct {
			id    model.ID
			cause errs.CorruptCause
		}{
			{2, errs.CorruptCause{Reason: errs.CorruptNotJSON, Detail: "not valid JSON: unexpected end of input"}},
			{3, errs.CorruptCause{Reason: errs.CorruptNotJSON, Detail: "empty"}},
			{4, errs.CorruptCause{Reason: errs.CorruptInvalid, Problems: []errs.Problem{{Field: "/id", Reason: "must match the ID in the filename (4)"}}}},
		} {
			ld := tx.Load(Location{"/proj", tc.id})
			if ld.State != Corrupt || !reflect.DeepEqual(ld.Cause, tc.cause) {
				t.Errorf("%d: %+v", tc.id, ld)
			}
			wantErr(t, tx.Needed(ld), errs.KindCorrupt, errs.CorruptDetails{
				Path: tx.Path(ld.Loc.Rel()), Reason: tc.cause.Reason, Problems: tc.cause.Problems, Detail: tc.cause.Detail,
			})
			wantNoErr(t, tx.Relevant(ld))
		}

		ld = tx.Load(Location{"/proj", 5})
		if ld.State != Unsupported || ld.Found != 2 {
			t.Errorf("5: %+v", ld)
		}
		wantErr(t, tx.Needed(ld), errs.KindUnsupportedFormat,
			errs.UnsupportedFormatDetails{Path: tx.Path("proj/5.json"), Found: 2, Supported: []int64{1}})
		wantNoErr(t, tx.Relevant(ld))

		ld = tx.Load(Location{"/proj", 6})
		if ld.State != Unreadable {
			t.Errorf("6: %+v", ld)
		}
		wantErr(t, tx.Needed(ld), errs.KindIO, errs.IODetails{Path: tx.Path("proj/6.json"), Code: "EIO"})
		wantNoErr(t, tx.Relevant(ld))

		ld = tx.Load(Location{"/proj", 8})
		if ld.State != Vanished {
			t.Errorf("8: %+v", ld)
		}
		wantNoErr(t, tx.Needed(ld))
		wantNoErr(t, tx.Relevant(ld))
	})
	p := func(rel string) string { return f.root + "/" + rel }
	want := []errs.Warning{
		errs.CorruptFile(p("proj/2.json"), 2),
		errs.CorruptFile(p("proj/3.json"), 3),
		errs.CorruptFile(p("proj/4.json"), 4),
		errs.UnsupportedFile(p("proj/5.json"), 5),
		errs.UnreadableFile(p("proj/6.json"), 6, "EIO"),
	}
	got := w.Warnings()
	if len(got) != len(want) {
		t.Fatalf("warnings %+v", got)
	}
	for i := range want {
		if got[i].Paths[0] != want[i].Paths[0] || got[i].Reason != want[i].Reason || got[i].Code != want[i].Code {
			t.Errorf("warning %d: %+v, want %+v", i, got[i], want[i])
		}
	}
}

// A task file is read once per transaction, however often it is loaded.
func TestLoadCached(t *testing.T) {
	f := newFixture(t)
	f.task("", 1, false)
	var ops []fsys.Op
	readTx(t, f.withFault(fsys.Record(&ops)), nil, func(tx *Tx) {
		a := tx.Load(Location{"/", 1})
		b := tx.Load(Location{"/", 1})
		if a != b {
			t.Error("loaded twice")
		}
	})
	n := 0
	for _, op := range ops {
		if op.Name == fsys.OpReadFile && op.Path == "1.json" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("read %d times", n)
	}
}

// NextStep drops the index and the cache, so a composed command's next
// operation sees what the earlier ones wrote.
func TestNextStep(t *testing.T) {
	f := newFixture(t)
	f.task("", 1, false)
	data, err := os.ReadFile(f.root + "/1.json")
	must(t, err)
	writeTx(t, f.env, nil, func(tx *Tx) {
		if got := tx.Index().Locations(2); got != nil {
			t.Fatalf("2 indexed before it exists: %v", got)
		}
		tx.Load(Location{"/", 1})
		wantNoErr(t, tx.Create("2.json", data))
		wantNoErr(t, tx.Replace("1.json", []byte("not json")))
		if tx.Index().Locations(2) != nil || tx.Load(Location{"/", 1}).State != Usable {
			t.Fatal("writes seen before NextStep")
		}
		tx.NextStep()
		if got := tx.Index().Locations(2); len(got) != 1 {
			t.Errorf("2 after NextStep: %v", got)
		}
		if ld := tx.Load(Location{"/", 1}); ld.State != Corrupt {
			t.Errorf("1 after NextStep: %+v", ld)
		}
	})
}

// A symlink swapped in for a task file is unreadable (ELOOP), not followed.
func TestLoadSymlink(t *testing.T) {
	f := newFixture(t)
	f.task("", 1, false)
	must(t, os.Symlink("1.json", f.root+"/2.json"))
	readTx(t, f.env, nil, func(tx *Tx) {
		ld := tx.Load(Location{"/", 2})
		if ld.State != Unreadable {
			t.Fatalf("%+v", ld)
		}
		wantErr(t, tx.Needed(ld), errs.KindIO, errs.IODetails{Path: tx.Path("2.json"), Code: "ELOOP"})
	})
}

// TestCorruptCause checks that a corrupt task file's error says what is
// wrong: every problem, or why it isn't JSON.
func TestCorruptCause(t *testing.T) {
	f := newFixture(t)
	f.task("", 1, false)
	valid := f.read("tasks/1.json")
	edit := func(old, new string) string {
		t.Helper()
		if !strings.Contains(valid, old) {
			t.Fatalf("no %q in %s", old, valid)
		}
		return strings.Replace(valid, old, new, 1)
	}
	var keys strings.Builder
	for i := 1; i <= 25; i++ {
		fmt.Fprintf(&keys, "  \"k%02d\": 0,\n", i)
	}
	const syntax = "not valid JSON: invalid character '}' looking for beginning of object key string (at byte 13)"
	var truncated []errs.Problem
	for i := 1; i <= errs.MaxProblems; i++ {
		truncated = append(truncated, errs.Problem{Field: fmt.Sprintf("/k%02d", i), Reason: "unknown field"})
	}

	for _, tc := range []struct {
		name, content string
		want          errs.CorruptDetails // Path is filled in
		message       string              // after the path
	}{
		{"missing updated_at", edit("  \"updated_at\": \"2026-09-20T18:31:51Z\",\n", ""),
			errs.CorruptDetails{Reason: errs.CorruptInvalid, Problems: []errs.Problem{{Field: "/updated_at", Reason: "required"}}},
			`corrupt: at "/updated_at": required`},
		{"unknown key", edit("{\n", "{\n  \"colour\": \"red\",\n"),
			errs.CorruptDetails{Reason: errs.CorruptInvalid, Problems: []errs.Problem{{Field: "/colour", Reason: "unknown field"}}},
			`corrupt: at "/colour": unknown field`},
		{"bad timestamp", edit("\"created_at\": \"2026-09-20T18:31:51Z\"", "\"created_at\": \"2026-02-30T18:31:51Z\""),
			errs.CorruptDetails{Reason: errs.CorruptInvalid, Problems: []errs.Problem{{Field: "/created_at", Reason: "is not a real date and time"}}},
			`corrupt: at "/created_at": is not a real date and time`},
		{"own ID in blocked_by", edit("\"blocked_by\": []", "\"blocked_by\": [\n    1\n  ]"),
			errs.CorruptDetails{Reason: errs.CorruptInvalid, Problems: []errs.Problem{{Field: "/blocked_by/0", Reason: "must not be the task's own ID"}}},
			`corrupt: at "/blocked_by/0": must not be the task's own ID`},
		{"several problems", edit("  \"updated_at\": \"2026-09-20T18:31:51Z\",\n", "  \"colour\": 1,\n"),
			errs.CorruptDetails{Reason: errs.CorruptInvalid, Problems: []errs.Problem{
				{Field: "/colour", Reason: "unknown field"}, {Field: "/updated_at", Reason: "required"}}},
			`corrupt: at "/colour": unknown field (and 1 more)`},
		{"too many problems", edit("{\n", "{\n"+keys.String()),
			errs.CorruptDetails{Reason: errs.CorruptInvalid, Problems: truncated, ProblemsTruncated: true},
			`corrupt: at "/k01": unknown field (and 24 more)`},
		{"empty", "",
			errs.CorruptDetails{Reason: errs.CorruptNotJSON, Detail: "empty"}, "corrupt: empty"},
		{"byte-order mark", "\xEF\xBB\xBF" + valid,
			errs.CorruptDetails{Reason: errs.CorruptNotJSON, Detail: "starts with a byte-order mark"}, "corrupt: starts with a byte-order mark"},
		{"syntax error", `{"schema": 1,}`,
			errs.CorruptDetails{Reason: errs.CorruptNotJSON, Detail: syntax}, "corrupt: " + syntax},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.write("tasks/1.json", tc.content)
			readTx(t, f.env, nil, func(tx *Tx) {
				e := tx.Needed(tx.Load(Location{"/", 1}))
				tc.want.Path = tx.Path("1.json")
				wantErr(t, e, errs.KindCorrupt, tc.want)
				if want := tc.want.Path + ": " + tc.message; e != nil && e.Message != want {
					t.Errorf("message %q, want %q", e.Message, want)
				}
			})
		})
	}
}
