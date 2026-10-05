package ops

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/koan/internal/fsys"
)

// link makes the file at newRel, under the home, a hard link to oldRel's.
func (f *fixture) link(oldRel, newRel string) {
	f.t.Helper()
	if err := os.Link(filepath.Join(f.home, oldRel), filepath.Join(f.home, newRel)); err != nil {
		f.t.Fatal(err)
	}
}

// entries is every entry under the root but koan.json, as a sorted list of
// paths, a folder's with a trailing "/": temp entries and symlinks included,
// nothing read.
func (f *fixture) entries() string {
	f.t.Helper()
	var out []string
	err := filepath.WalkDir(f.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == f.root {
			return err
		}
		rel, _ := filepath.Rel(f.root, p)
		if rel == "koan.json" {
			return nil
		}
		if d.IsDir() {
			rel += "/"
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return strings.Join(out, " ")
}

func TestDoctorHealthy(t *testing.T) {
	f := newFixture(t)
	f.task("p", 1, false)
	f.write("tasks/p/1.md", "notes")
	f.task("", 2, true, 1)
	f.write("tasks/.git/HEAD", "x")       // hidden, not koan's: ignored
	f.write("tasks/.DS_Store", "x")       // likewise
	f.write("tasks/.git/.koan-tmp-x", "") // inside a hidden folder: not looked at
	if got, want := f.op("doctor", `{}`), `{"healthy":true,"findings":[]}`; got != want {
		t.Errorf("doctor: got  %s\nwant %s", got, want)
	}
	if got, want := f.op("repair", `{}`), `{"repaired":[],"healthy":true,"findings":[]}`; got != want {
		t.Errorf("repair: got  %s\nwant %s", got, want)
	}
}

// Each finding kind, from a tree built by hand: the exact findings doctor
// reports for it, and what repair leaves (implementation-spec.md, doctor
// and repair tests).
func TestDoctorKinds(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(f *fixture)
		// doctor is doctor's output for the tree.
		doctor string
		// files is the tree after repair, as fixture.entries lists it; "" when
		// repair fails.
		files string
	}{
		{
			name: "temp-leftover",
			setup: func(f *fixture) {
				f.write("tasks/.koan-tmp-a", "x")
				f.write("tasks/p/.koan-tmp-b/1.json", "x") // a folder renamed aside: not descended into
			},
			doctor: `{"healthy":false,"findings":[{"kind":"temp-leftover","class":"auto","count":2,"truncated":false,"items":[` +
				`{"paths":["~/tasks/.koan-tmp-a"],"ids":[],"action":"remove","suggest":"koan repair"},` +
				`{"paths":["~/tasks/p/.koan-tmp-b"],"ids":[],"action":"remove","suggest":"koan repair"}]}]}`,
			files: "p/",
		},
		{
			name: "id-above-last-id",
			setup: func(f *fixture) {
				f.task("", 101, false)
				f.write("tasks/p/150.json", "{") // unusable, but it occupies its ID
			},
			doctor: `{"healthy":false,"findings":[{"kind":"id-above-last-id","class":"auto","count":2,"truncated":false,"items":[` +
				`{"paths":["~/tasks/101.json"],"ids":[101],"action":"raise-last-id","suggest":"koan repair","last_id":150},` +
				`{"paths":["~/tasks/p/150.json"],"ids":[150],"action":"raise-last-id","suggest":"koan repair","last_id":150}]},` +
				`{"kind":"unusable-file","class":"manual","count":1,"truncated":false,"items":[{"paths":["~/tasks/p/150.json"],"ids":[150],"action":null,"suggest":"fix the file by hand, or restore it from git",` +
				`"error":{"kind":"corrupt","message":"~/tasks/p/150.json: corrupt: not valid JSON: unexpected end of input","details":{"path":"~/tasks/p/150.json","reason":"not-json","detail":"not valid JSON: unexpected end of input"}}}]}]}`,
			files: "101.json p/ p/150.json",
		},
		{
			name: "dangling-reference",
			setup: func(f *fixture) {
				f.task("", 1, false, 2, 3, 4)
				f.task("", 3, true)
				f.write("tasks/4.json", "{") // exists, though unusable: not dangling
			},
			doctor: `{"healthy":false,"findings":[{"kind":"dangling-reference","class":"auto","count":1,"truncated":false,"items":[` +
				`{"paths":["~/tasks/1.json"],"ids":[1,2],"action":"remove-reference","suggest":"koan repair"}]},` +
				`{"kind":"unusable-file","class":"manual","count":1,"truncated":false,"items":[{"paths":["~/tasks/4.json"],"ids":[4],"action":null,"suggest":"fix the file by hand, or restore it from git",` +
				`"error":{"kind":"corrupt","message":"~/tasks/4.json: corrupt: not valid JSON: unexpected end of input","details":{"path":"~/tasks/4.json","reason":"not-json","detail":"not valid JSON: unexpected end of input"}}}]}]}`,
			files: "1.json 3.json 4.json",
		},
		{
			name: "orphan-notes",
			setup: func(f *fixture) {
				f.write("tasks/4.md", "")
				f.write("tasks/5.md", "text")
				f.task("p", 6, false)
				f.write("tasks/p/6.md", "six")
				f.link("tasks/p/6.md", "tasks/6.md") // what an interrupted move leaves
				f.task("p", 7, false)
				f.write("tasks/p/7.md", "seven")
				f.write("tasks/7.md", "seven, saved late")
			},
			doctor: `{"healthy":false,"findings":[{"kind":"orphan-notes","class":"auto","count":4,"truncated":false,"items":[` +
				`{"paths":["~/tasks/4.md"],"ids":[4],"action":"remove","suggest":"koan repair","reason":"empty"},` +
				`{"paths":["~/tasks/5.md"],"ids":[5],"action":null,"suggest":"keep its text elsewhere if it is still wanted, then remove it","reason":"no-task"},` +
				`{"paths":["~/tasks/6.md","~/tasks/p/6.json"],"ids":[6],"action":"remove","suggest":"koan repair","reason":"linked"},` +
				`{"paths":["~/tasks/7.md","~/tasks/p/7.json"],"ids":[7],"action":null,"suggest":"merge its text into ~/tasks/p/7.md, then remove it","reason":"task-elsewhere"}]}]}`,
			files: "5.md 7.md p/ p/6.json p/6.md p/7.json p/7.md",
		},
		{
			name: "duplicate-id",
			setup: func(f *fixture) {
				f.task("", 8, false)
				f.task("p", 8, false)
				f.task("", 9, false)
				f.task("p", 9, true)
			},
			doctor: `{"healthy":false,"findings":[{"kind":"duplicate-id","class":"manual","count":2,"truncated":false,"items":[` +
				`{"paths":["~/tasks/8.json","~/tasks/p/8.json"],"ids":[8],"action":null,"suggest":"the copies are the same: remove all but one","identical":true},` +
				`{"paths":["~/tasks/9.json","~/tasks/p/9.json"],"ids":[9],"action":null,"suggest":"keep the copy that is right, and remove the others, or move their task to a new ID with koan create","identical":false}]}]}`,
			files: "8.json 9.json p/ p/8.json p/9.json",
		},
		{
			name: "cycle",
			setup: func(f *fixture) {
				f.task("", 10, false, 11)
				f.task("", 11, true, 12) // complete tasks count too
				f.task("p", 12, false, 10, 13)
				f.task("p", 13, false)
				f.task("q", 20, false, 21)
				f.task("q", 21, false, 20)
			},
			doctor: `{"healthy":false,"findings":[{"kind":"cycle","class":"manual","count":2,"truncated":false,"items":[` +
				`{"paths":[],"ids":[10,11,12,10],"action":null,"suggest":"remove any one blocker on the cycle, e.g. koan unblock 10 --blockers 11","group":[10,11,12]},` +
				`{"paths":[],"ids":[20,21,20],"action":null,"suggest":"remove any one blocker on the cycle, e.g. koan unblock 20 --blockers 21","group":[20,21]}]}]}`,
			files: "10.json 11.json p/ p/12.json p/13.json q/ q/20.json q/21.json",
		},
		{
			name: "nested-tree and skipped-entry",
			setup: func(f *fixture) {
				f.write("tasks/p/koan.json", `{"schema": 1, "last_id": 3}`)
				f.write("tasks/notes.txt", "") // stray-entry: not reported unless asked for
				if err := os.MkdirAll(filepath.Join(f.root, "18.json"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("p", filepath.Join(f.root, "lnk")); err != nil {
					t.Fatal(err)
				}
			},
			doctor: `{"healthy":false,"findings":[{"kind":"nested-tree","class":"manual","count":1,"truncated":false,"items":[` +
				`{"paths":["~/tasks/p/koan.json"],"ids":[],"action":null,"suggest":"move the tree it belongs to out of this one, or remove this koan.json if the folder is part of this tree"}]},` +
				`{"kind":"skipped-entry","class":"manual","count":2,"truncated":false,"items":[` +
				`{"paths":["~/tasks/18.json"],"ids":[],"action":null,"suggest":"rename or remove it: it has a folder's or task file's name, but the wrong type, so koan skips it","reason":"type"},` +
				`{"paths":["~/tasks/lnk"],"ids":[],"action":null,"suggest":"koan never follows a symlink, so what it leads to is not part of the tree: move the real folder or file in instead, or remove the link","reason":"symlink"}]}]}`,
			files: "18.json/ lnk notes.txt p/ p/koan.json",
		},
		{
			name: "metadata-missing",
			setup: func(f *fixture) {
				f.remove("tasks/koan.json")
				f.task("p", 9, false)
				f.write("tasks/.koan-tmp-a", "")
			},
			doctor: `{"healthy":false,"findings":[{"kind":"metadata-missing","class":"on-request","count":1,"truncated":false,"items":[` +
				`{"paths":["~/tasks/koan.json"],"ids":[],"action":"create-metadata","suggest":"koan repair --kinds metadata-missing, unless a task with an ID above 9 was ever deleted; then rebuild koan.json by hand with that ID as last_id","last_id":9}]},` +
				`{"kind":"temp-leftover","class":"auto","count":1,"truncated":false,"items":[{"paths":["~/tasks/.koan-tmp-a"],"ids":[],"action":"remove","suggest":"koan repair"}]}]}`,
		},
		{
			name: "metadata-unusable",
			setup: func(f *fixture) {
				f.write("tasks/koan.json", `{"schema": 2, "last_id": 1}`)
				f.task("", 5, false, 6)
			},
			doctor: `{"healthy":false,"findings":[{"kind":"dangling-reference","class":"auto","count":1,"truncated":false,"items":[{"paths":["~/tasks/5.json"],"ids":[5,6],"action":"remove-reference","suggest":"koan repair"}]},` +
				`{"kind":"metadata-unusable","class":"manual","count":1,"truncated":false,"items":[` +
				`{"paths":["~/tasks/koan.json"],"ids":[],"action":null,"suggest":"fix koan.json by hand, or restore it from git; a binary that supports its format can use it as it is",` +
				`"error":{"kind":"unsupported-format","message":"~/tasks/koan.json: format version 2 is not supported","details":{"path":"~/tasks/koan.json","found":2,"supported":[1]}}}]}]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.setup(f)
			if got := f.op("doctor", `{}`); got != tc.doctor {
				t.Errorf("doctor:\ngot  %s\nwant %s", got, tc.doctor)
			}
			if tc.files == "" {
				return
			}
			out := f.op("repair", `{}`)
			if got := f.entries(); got != tc.files {
				t.Errorf("after repair: %s\nwant %s\nrepair: %s", got, tc.files, out)
			}
		})
	}
}

// A user's own files and an editor's backups are stray entries: listed only
// when asked for, and never making the tree unhealthy; repair refuses them.
func TestDoctorStrayEntry(t *testing.T) {
	f := newFixture(t)
	f.task("", 1, false)
	f.write("tasks/README.md", "my tasks")
	f.write("tasks/1.md~", "")
	f.write("tasks/#1.md#", "")
	if got, want := f.op("doctor", `{}`), `{"healthy":true,"findings":[]}`; got != want {
		t.Errorf("doctor: got  %s\nwant %s", got, want)
	}
	want := `{"healthy":true,"findings":[{"kind":"stray-entry","class":"informational","count":3,"truncated":false,"items":[` +
		`{"paths":["~/tasks/#1.md#"],"ids":[],"action":null,"suggest":"nothing to do: koan ignores it"},` +
		`{"paths":["~/tasks/1.md~"],"ids":[],"action":null,"suggest":"nothing to do: koan ignores it"},` +
		`{"paths":["~/tasks/README.md"],"ids":[],"action":null,"suggest":"nothing to do: koan ignores it"}]}]}`
	if got := f.op("doctor", `{"kinds": ["stray-entry"]}`); got != want {
		t.Errorf("doctor, asked for:\ngot  %s\nwant %s", got, want)
	}
	if got, want := f.op("repair", `{}`), `{"repaired":[],"healthy":true,"findings":[]}`; got != want {
		t.Errorf("repair: got  %s\nwant %s", got, want)
	}
	if got := f.op("repair", `{"kinds": ["stray-entry"]}`); !strings.Contains(got, `"reason":"stray-entry is informational: repair never changes it; see koan doctor --kinds stray-entry"`) {
		t.Errorf("repair, asked for: %s", got)
	}
}

// After repair, doctor finds only what repair leaves to a person; rerunning
// repair changes nothing.
func TestRepair(t *testing.T) {
	f := newFixture(t)
	f.write("tasks/.koan-tmp-a", "x")
	f.task("", 101, false)
	f.task("", 1, false, 2, 3)
	f.task("p", 3, false)
	f.write("tasks/4.md", "")
	f.write("tasks/5.md", "text")
	f.task("", 10, false, 11)
	f.task("", 11, false, 10)
	got := f.op("repair", `{}`)
	want := `{"repaired":[` +
		`{"kind":"dangling-reference","class":"auto","count":1,"truncated":false,"items":[{"paths":["~/tasks/1.json"],"ids":[1,2],"action":"remove-reference","suggest":"koan repair"}]},` +
		`{"kind":"id-above-last-id","class":"auto","count":1,"truncated":false,"items":[{"paths":["~/tasks/101.json"],"ids":[101],"action":"raise-last-id","suggest":"koan repair","last_id":101}]},` +
		`{"kind":"orphan-notes","class":"auto","count":1,"truncated":false,"items":[{"paths":["~/tasks/4.md"],"ids":[4],"action":"remove","suggest":"koan repair","reason":"empty"}]},` +
		`{"kind":"temp-leftover","class":"auto","count":1,"truncated":false,"items":[{"paths":["~/tasks/.koan-tmp-a"],"ids":[],"action":"remove","suggest":"koan repair"}]}],` +
		`"healthy":false,"findings":[` +
		`{"kind":"cycle","class":"manual","count":1,"truncated":false,"items":[{"paths":[],"ids":[10,11,10],"action":null,"suggest":"remove any one blocker on the cycle, e.g. koan unblock 10 --blockers 11","group":[10,11]}]},` +
		`{"kind":"orphan-notes","class":"auto","count":1,"truncated":false,"items":[{"paths":["~/tasks/5.md"],"ids":[5],"action":null,"suggest":"keep its text elsewhere if it is still wanted, then remove it","reason":"no-task"}]}]}`
	if got != want {
		t.Errorf("repair:\ngot  %s\nwant %s", got, want)
	}
	if got := f.read("tasks/koan.json"); got != "{\n  \"schema\": 1,\n  \"last_id\": 101\n}\n" {
		t.Errorf("koan.json %q", got)
	}
	if got := f.blockedBy("1.json") + " " + f.updatedAt("1.json"); got != "[3] 2026-09-28T12:00:00Z" {
		t.Errorf("1.json: %s", got)
	}
	if got, want := f.files(), "1.json 10.json 101.json 11.json 5.md p/ p/3.json"; got != want {
		t.Errorf("files %s, want %s", got, want)
	}
	again := f.op("repair", `{}`)
	if !strings.HasPrefix(again, `{"repaired":[],"healthy":false,`) {
		t.Errorf("rerun: %s", again)
	}
}

// repair of only the kinds named; each named kind is listed in full.
func TestRepairKinds(t *testing.T) {
	f := newFixture(t)
	f.write("tasks/.koan-tmp-a", "")
	f.write("tasks/4.md", "")
	got := f.op("repair", `{"kinds": ["orphan-notes"]}`)
	if !strings.Contains(got, `"repaired":[{"kind":"orphan-notes"`) || !strings.Contains(got, `"findings":[{"kind":"temp-leftover"`) {
		t.Errorf("repair: %s", got)
	}
	if got, want := f.entries(), ".koan-tmp-a"; got != want {
		t.Errorf("files %s, want %s", got, want)
	}
}

func TestRepairPreconditions(t *testing.T) {
	for _, tc := range []struct {
		name, meta, input, want string
	}{
		{"manual kind", "", `{"kinds": ["temp-leftover", "cycle"]}`,
			`invalid-input {"problems":[{"field":"/kinds/1","reason":"cycle is manual: repair never changes it; see koan doctor --kinds cycle"}]}`},
		{"unknown kind", "", `{"kinds": ["nope"]}`, `invalid-input`},
		{"missing", "<none>", `{}`, `not-initialized {"missing":"metadata"}`},
		{"corrupt", "{", `{}`, `corrupt {"path":"~/tasks/koan.json","reason":"not-json","detail":"not valid JSON: unexpected end of input"}`},
		{"unsupported", `{"schema": 2, "last_id": 1}`, `{}`, `unsupported-format {"path":"~/tasks/koan.json","found":2,"supported":[1]}`},
		{"missing, named", "<none>", `{"kinds": ["metadata-missing"]}`, `{"repaired":[{"kind":"metadata-missing"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.task("p", 7, false, 99)
			f.write("tasks/.koan-tmp-a", "")
			switch tc.meta {
			case "":
			case "<none>":
				f.remove("tasks/koan.json")
			default:
				f.write("tasks/koan.json", tc.meta)
			}
			before := f.files() + f.read("tasks/koan.json") + f.read("tasks/p/7.json")
			got := f.op("repair", tc.input)
			if !strings.HasPrefix(got, tc.want) {
				t.Errorf("got  %s\nwant %s…", got, tc.want)
			}
			if strings.HasPrefix(got, "{") {
				return
			}
			if after := f.files() + f.read("tasks/koan.json") + f.read("tasks/p/7.json"); after != before {
				t.Errorf("changed:\n%s\n%s", before, after)
			}
		})
	}
}

// A rebuilt koan.json gets the highest ID in any task filename; the other
// auto kinds named with it are repaired against it.
func TestRepairMetadataMissing(t *testing.T) {
	f := newFixture(t)
	f.remove("tasks/koan.json")
	f.task("p", 7, false, 99)
	f.write("tasks/12.json", "{")
	f.op("repair", `{"kinds": ["metadata-missing", "dangling-reference"]}`)
	if got := f.read("tasks/koan.json"); got != "{\n  \"schema\": 1,\n  \"last_id\": 12\n}\n" {
		t.Errorf("koan.json %q", got)
	}
	if got := f.blockedBy("p/7.json"); got != "[]" {
		t.Errorf("7.json blocked_by %s", got)
	}
}

// koan.json is not rebuilt while a folder can't be listed: a task in it may
// have a higher ID, which a too-low last_id would reissue.
func TestRepairMetadataMissingUnreadable(t *testing.T) {
	f := newFixture(t)
	f.remove("tasks/koan.json")
	f.task("", 3, false)
	f.task("q", 4, false)
	f.fail(fsys.OpReadDir, "q", syscall.EACCES)
	got := f.op("repair", `{"kinds": ["metadata-missing"]}`)
	want := `{"repaired":[],"healthy":false,"findings":[{"kind":"metadata-missing","class":"on-request","count":1,"truncated":false,"items":[` +
		`{"paths":["~/tasks/koan.json"],"ids":[],"action":null,"suggest":"fix the unreadable folders first; then koan repair --kinds metadata-missing","last_id":3}]},`
	if !strings.HasPrefix(got, want) {
		t.Errorf("repair:\ngot  %s\nwant %s...", got, want)
	}
	if got := f.read("tasks/koan.json"); got != "<none>" {
		t.Errorf("koan.json %q", got)
	}
}

// No dangling reference is reported, or removed, while a folder can't be
// listed: the blocker may be in it.
func TestDoctorUnreadableFolder(t *testing.T) {
	f := newFixture(t)
	f.task("", 1, false, 2)
	f.task("q", 2, false)
	f.fail(fsys.OpReadDir, "q", syscall.EACCES)
	want := `{"healthy":false,"findings":[{"kind":"unreadable-folder","class":"manual","count":1,"truncated":false,"items":[` +
		`{"paths":["~/tasks/q"],"ids":[],"action":null,"suggest":"fix its permissions; until then its tasks are missing from every result","code":"EACCES"}]}]}`
	if got := f.op("doctor", `{}`); got != want {
		t.Errorf("doctor:\ngot  %s\nwant %s", got, want)
	}
	f.op("repair", `{}`)
	if got := f.blockedBy("1.json"); got != "[2]" {
		t.Errorf("1.json blocked_by %s", got)
	}
}

// An orphaned .md that can't be looked at, e.g. in a folder that can be
// listed but not searched, is reported with its code, never passed over as
// healthy; repair leaves it.
func TestDoctorOrphanUnreadable(t *testing.T) {
	f := newFixture(t)
	f.write("tasks/e/77.md", "hi\n")
	f.fail(fsys.OpLstat, "e/77.md", syscall.EACCES)
	want := `{"healthy":false,"findings":[{"kind":"orphan-notes","class":"auto","count":1,"truncated":false,"items":[` +
		`{"paths":["~/tasks/e/77.md"],"ids":[77],"action":null,"suggest":"fix its permissions, then run koan doctor again to see what it is","reason":"unreadable","code":"EACCES"}]}]}`
	if got := f.op("doctor", `{}`); got != want {
		t.Errorf("doctor:\ngot  %s\nwant %s", got, want)
	}
	f.op("repair", `{}`)
	if got := f.read("tasks/e/77.md"); got != "hi\n" {
		t.Errorf("77.md %q", got)
	}
}

// At most 20 items a kind, unless the input names the kind.
func TestDoctorCap(t *testing.T) {
	f := newFixture(t)
	for i := range 25 {
		f.write(fmt.Sprintf("tasks/.koan-tmp-%02d", i), "")
	}
	f.write("tasks/x.txt", "")
	short := f.op("doctor", `{}`)
	if !strings.Contains(short, `"kind":"temp-leftover","class":"auto","count":25,"truncated":true`) || strings.Count(short, `"action":"remove"`) != 20 {
		t.Errorf("capped: %s", short)
	}
	full := f.op("doctor", `{"kinds": ["temp-leftover"]}`)
	if !strings.Contains(full, `"count":25,"truncated":false`) || strings.Count(full, `"action":"remove"`) != 25 || strings.Contains(full, "stray-entry") {
		t.Errorf("named: %s", full)
	}
	if !strings.Contains(full, `"healthy":false`) {
		t.Errorf("healthy counts every kind: %s", full)
	}
}

// An orphaned .md that changes between the check and its removal is left,
// and reported: the change is made at repair's second Lstat of each, the one
// just before the removal.
func TestRepairRechecksOrphans(t *testing.T) {
	f := newFixture(t)
	f.write("tasks/4.md", "")
	f.task("p", 6, false)
	f.write("tasks/p/6.md", "six")
	f.link("tasks/p/6.md", "tasks/6.md")
	seen := map[string]int{}
	f.hook(func(o fsys.Op) error {
		if o.Name != fsys.OpLstat {
			return nil
		}
		seen[o.Path]++
		if seen[o.Path] != 2 {
			return nil
		}
		switch o.Path {
		case "4.md":
			f.write("tasks/4.md", "now with text")
		case "6.md": // a copy now, not a second name
			f.remove("tasks/6.md")
			f.write("tasks/6.md", "six")
		}
		return nil
	})
	got := f.op("repair", `{"kinds": ["orphan-notes"]}`)
	if !strings.HasPrefix(got, `{"repaired":[],"healthy":false,`) || !strings.Contains(got, `"reason":"no-task"`) || !strings.Contains(got, `"reason":"task-elsewhere"`) {
		t.Errorf("repair: %s", got)
	}
	if got, want := f.entries(), "4.md 6.md p/ p/6.json p/6.md"; got != want {
		t.Errorf("entries %s, want %s", got, want)
	}
}
