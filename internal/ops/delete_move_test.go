package ops

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/ftask/internal/fsys"
	"github.com/phansen314/ftask/internal/jsonio"
	"github.com/phansen314/ftask/internal/schematest"
)

// op runs op with input over f, checking the envelope and, on success,
// op-output. It returns the result as compact JSON, or the error as "kind
// details", followed by " partial <json>" when there is one, then by each
// warning's " warn kind"; the home as "~".
func (f *fixture) op(op, input string) string {
	f.t.Helper()
	e := Run(op, parse(f.t, input), nil, f.env)
	line(f.t, e)
	enc := func(v any) string {
		b, err := jsonio.MarshalLine(v)
		if err != nil {
			f.t.Fatal(err)
		}
		return strings.TrimSpace(string(b))
	}
	var s string
	if e.OK {
		s = enc(e.Result)
		if ok, fl := schematest.Check(f.t, op+"-output", []byte(s)); !ok {
			f.t.Errorf("%s-output rejects at %s: %s", op, fl, s)
		}
	} else {
		s = string(e.Error.Kind) + " " + enc(e.Error.Details)
		if e.Error.Partial != nil {
			p := enc(e.Error.Partial)
			if ok, fl := schematest.Check(f.t, op+"-partial", []byte(p)); !ok {
				f.t.Errorf("%s-partial rejects at %s: %s", op, fl, p)
			}
			s += " partial " + p
		}
	}
	for _, w := range e.Warnings {
		s += " warn " + string(w.Kind)
	}
	return f.rel(s)
}

// files is every entry under the root but ftask.json, as a sorted list of
// paths, a folder's with a trailing "/"; temp entries included, so a
// leftover shows.
func (f *fixture) files() string {
	f.t.Helper()
	all := tree(f.t, f.home)
	var out []string
	for _, p := range slices.Sorted(maps.Keys(all)) {
		rel, ok := strings.CutPrefix(p, "tasks/")
		if !ok || rel == "ftask.json" {
			continue
		}
		if all[p] == "/" {
			rel += "/"
		}
		out = append(out, rel)
	}
	return strings.Join(out, " ")
}

func (f *fixture) blockedBy(rel string) string {
	f.t.Helper()
	s := f.read("tasks/" + rel)
	i := strings.Index(s, `"blocked_by": `)
	if i < 0 {
		return s
	}
	s = s[i+len(`"blocked_by": `):]
	return strings.Join(strings.Fields(s[:strings.IndexAny(s, "]")+1]), "")
}

// updatedAt is the updated_at of the task file at rel under the root.
func (f *fixture) updatedAt(rel string) string {
	f.t.Helper()
	m := regexp.MustCompile(`"updated_at": "([^"]*)"`).FindStringSubmatch(f.read("tasks/" + rel))
	if m == nil {
		return ""
	}
	return m[1]
}

func TestDelete(t *testing.T) {
	f := newFixture(t)
	f.task("p", 5, false)
	f.write("tasks/p/5.md", "notes\n")
	f.task("", 6, false, 5, 7)
	f.task("", 7, true)
	f.task("q", 8, true, 5)
	if got, want := f.op("delete", `{"id": 5}`), `{"id":5,"folder":"/p","dependents":[6,8]}`; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if got, want := f.files(), "6.json 7.json p/ q/ q/8.json"; got != want {
		t.Errorf("files %s, want %s", got, want)
	}
	if got := f.blockedBy("6.json") + " " + f.blockedBy("q/8.json"); got != "[7] []" {
		t.Errorf("blocked_by %s", got)
	}
	// Only the dependents rewritten are stamped.
	if got, want := f.updatedAt("6.json")+" "+f.updatedAt("7.json")+" "+f.updatedAt("q/8.json"),
		"2026-09-28T12:00:00Z 2026-09-20T18:31:51Z 2026-09-28T12:00:00Z"; got != want {
		t.Errorf("updated_at %s, want %s", got, want)
	}
}

func TestDeleteCases(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		setup       func(f *fixture)
		want, files string
	}{
		{"no dependents, no notes", `{"id": 5}`, func(f *fixture) { f.task("", 5, false); f.task("", 6, false) },
			`{"id":5,"folder":"/","dependents":[]}`, "6.json"},
		{"an unusable task is deleted without being read", `{"id": 5}`, func(f *fixture) { f.write("tasks/5.json", "{") },
			`{"id":5,"folder":"/","dependents":[]}`, ""},
		{"every other unusable file is a warning", `{"id": 5}`, func(f *fixture) { f.task("", 5, false); f.write("tasks/6.json", "{") },
			`{"id":5,"folder":"/","dependents":[]} warn unusable-file`, "6.json"},
		{"each copy of a duplicated dependent is rewritten", `{"id": 5}`, func(f *fixture) {
			f.task("", 5, false)
			f.task("a", 6, false, 5)
			f.task("b", 6, false, 5)
		}, `{"id":5,"folder":"/","dependents":[6]}`, "a/ a/6.json b/ b/6.json"},
		{"a stray .md elsewhere is left alone", `{"id": 5}`, func(f *fixture) { f.task("", 5, false); f.write("tasks/p/5.md", "x") },
			`{"id":5,"folder":"/","dependents":[]}`, "p/ p/5.md"},

		{"invalid", `{"id": 0}`, nil, `invalid-input {"problems":[{"field":"/id","reason":"must be between 1 and 999999999999999"}]}`, ""},
		{"not found", `{"id": 5}`, func(f *fixture) { f.task("", 6, false) },
			`not-found {"folders":[],"ids":[5],"paths":[]}`, "6.json"},
		{"duplicated", `{"id": 5}`, func(f *fixture) { f.task("", 5, false); f.task("p", 5, false) },
			`conflict {"rule":"duplicate-id","ids":[5]}`, "5.json p/ p/5.json"},
		{"duplicated, one copy can't be looked at", `{"id": 5}`, func(f *fixture) {
			f.task("", 5, false)
			f.task("p", 5, false)
			f.failFile("p/5.json", syscall.EACCES)
		}, `conflict {"rule":"duplicate-id","ids":[5]}`, "5.json p/ p/5.json"},
		{"above last_id", `{"id": 101}`, func(f *fixture) { f.task("", 101, false); f.task("", 6, false, 101) },
			`conflict {"rule":"id-above-last-id","ids":[101]}`, "101.json 6.json"},
		{"duplicated and above last_id: duplicated first", `{"id": 101}`, func(f *fixture) { f.task("", 101, false); f.task("p", 101, false) },
			`conflict {"rule":"duplicate-id","ids":[101]}`, "101.json p/ p/101.json"},
		{"a folder that can't be listed", `{"id": 5}`, func(f *fixture) {
			f.task("", 5, false)
			f.task("q", 6, false)
			f.fail(fsys.OpReadDir, "q", syscall.EACCES)
		}, `io {"path":"~/tasks/q","code":"EACCES"}`, "5.json q/ q/6.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			if tc.setup != nil {
				tc.setup(f)
			}
			if got := f.op("delete", tc.input); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
			if got := f.files(); got != tc.files {
				t.Errorf("files %q, want %q", got, tc.files)
			}
		})
	}
}

func TestDeleteFolder(t *testing.T) {
	f := newFixture(t)
	f.task("p/a", 5, false)
	f.write("tasks/p/a/5.md", "notes\n")
	f.task("p/a/b", 6, false, 5)
	f.write("tasks/p/a/b/.hidden", "x")
	f.write("tasks/p/a/b/6.md~", "x")
	f.task("q", 7, false, 5, 6, 9)
	f.task("", 9, false)
	got := f.op("delete-folder", `{"folder": "/p/a", "recursive": true}`)
	if want := `{"folder":"/p/a","folders":["/p/a","/p/a/b"],"ids":[5,6],"dependents":[7]}`; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if got, want := f.files(), "9.json p/ q/ q/7.json"; got != want {
		t.Errorf("files %s, want %s", got, want)
	}
	if got := f.blockedBy("q/7.json"); got != "[9]" {
		t.Errorf("blocked_by %s", got)
	}
	if got, want := f.updatedAt("q/7.json")+" "+f.updatedAt("9.json"), "2026-09-28T12:00:00Z 2026-09-20T18:31:51Z"; got != want {
		t.Errorf("updated_at %s, want %s", got, want)
	}
}

func TestDeleteFolderCases(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		setup       func(f *fixture)
		want, files string
	}{
		{"empty", `{"folder": "/p"}`, func(f *fixture) { f.write("tasks/p/.keep", ""); f.write("tasks/p/5.md", "") },
			`{"folder":"/p","folders":["/p"],"ids":[],"dependents":[]}`, ""},
		{"unusable task files go with it", `{"folder": "/p", "recursive": true}`, func(f *fixture) { f.write("tasks/p/5.json", "{"); f.task("", 6, false, 5) },
			`{"folder":"/p","folders":["/p"],"ids":[5],"dependents":[6]}`, "6.json"},
		{"references inside stay inside", `{"folder": "/p", "recursive": true}`, func(f *fixture) { f.task("p", 5, false); f.task("p", 6, false, 5) },
			`{"folder":"/p","folders":["/p"],"ids":[5,6],"dependents":[]}`, ""},
		{"unusable files outside are warnings", `{"folder": "/p", "recursive": true}`, func(f *fixture) { f.task("p", 5, false); f.write("tasks/6.json", "{") },
			`{"folder":"/p","folders":["/p"],"ids":[5],"dependents":[]} warn unusable-file`, "6.json"},
		{"no task: nothing outside read", `{"folder": "/p", "recursive": true}`, func(f *fixture) {
			f.write("tasks/p/a/.keep", "")
			f.write("tasks/6.json", "{")
			f.task("q", 7, false)
			f.fail(fsys.OpReadDir, "q", syscall.EACCES)
		}, `{"folder":"/p","folders":["/p","/p/a"],"ids":[],"dependents":[]}`, "6.json q/ q/7.json"},

		{"the root", `{"folder": "/"}`, nil, `invalid-input {"problems":[{"field":"/folder","reason":"the root can never be deleted"}]}`, ""},
		{"not found", `{"folder": "/p/a"}`, nil, `not-found {"folders":["/p"],"ids":[],"paths":[]}`, ""},
		{"not a folder", `{"folder": "/p"}`, func(f *fixture) { f.write("tasks/p", "") },
			`corrupt {"path":"~/tasks/p","reason":"unexpected-file"}`, "p"},
		{"not empty: tasks", `{"folder": "/p"}`, func(f *fixture) { f.task("p/a", 6, false); f.task("p", 5, false) },
			`conflict {"rule":"not-empty","ids":[5,6]}`, "p/ p/5.json p/a/ p/a/6.json"},
		{"not empty: a folder", `{"folder": "/p"}`, func(f *fixture) { f.write("tasks/p/a/.keep", "") },
			`conflict {"rule":"not-empty","ids":[]}`, "p/ p/a/ p/a/.keep"},
		{"not empty: a user's file", `{"folder": "/p"}`, func(f *fixture) { f.write("tasks/p/x.txt", "") },
			`conflict {"rule":"not-empty","ids":[]}`, "p/ p/x.txt"},
		{"not empty: notes with text and no task", `{"folder": "/p"}`, func(f *fixture) { f.write("tasks/p/77.md", "notes") },
			`conflict {"rule":"not-empty","ids":[]}`, "p/ p/77.md"},
		{"not empty: a folder that isn't ftask's", `{"folder": "/p"}`, func(f *fixture) { f.write("tasks/p/Photos/img.txt", "precious") },
			`conflict {"rule":"not-empty","ids":[]}`, "p/ p/Photos/ p/Photos/img.txt"},
		{"duplicated", `{"folder": "/p", "recursive": true}`, func(f *fixture) { f.task("p", 5, false); f.task("", 5, false); f.task("p", 6, false) },
			`conflict {"rule":"duplicate-id","ids":[5]}`, "5.json p/ p/5.json p/6.json"},
		{"above last_id", `{"folder": "/p", "recursive": true}`, func(f *fixture) { f.task("p", 101, false); f.task("p", 102, false) },
			`conflict {"rule":"id-above-last-id","ids":[101,102]}`, "p/ p/101.json p/102.json"},
		{"a folder inside that can't be listed", `{"folder": "/p", "recursive": true}`, func(f *fixture) {
			f.write("tasks/p/a/.keep", "")
			f.fail(fsys.OpReadDir, "p/a", syscall.EACCES)
		}, `io {"path":"~/tasks/p/a","code":"EACCES"}`, "p/ p/a/ p/a/.keep"},
		{"a folder outside that can't be listed, with tasks to dereference", `{"folder": "/p", "recursive": true}`, func(f *fixture) {
			f.task("p", 5, false)
			f.task("q", 6, false)
			f.fail(fsys.OpReadDir, "q", syscall.EACCES)
		}, `io {"path":"~/tasks/q","code":"EACCES"}`, "p/ p/5.json q/ q/6.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			if tc.setup != nil {
				tc.setup(f)
			}
			if got := f.op("delete-folder", tc.input); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
			if got := f.files(); got != tc.files {
				t.Errorf("files %q, want %q", got, tc.files)
			}
		})
	}
}

func TestMove(t *testing.T) {
	f := newFixture(t)
	f.task("", 5, false, 6)
	f.task("", 6, false)
	f.write("tasks/5.md", "notes\n")
	got := f.op("move", `{"id": 5, "to": "/p/q", "parents": true}`)
	want := `{"schema":1,"id":5,"title":"task 5","priority":null,"created_at":"2026-09-20T18:31:51Z","completed_at":null,"updated_at":"2026-09-20T18:31:51Z","blocked_by":[6],"tags":[],"extra":{},"folder":"/p/q","notes_path":"~/tasks/p/q/5.md","from":"/","created":["/p","/p/q"],"changed":true}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if got, want := f.files(), "6.json p/ p/q/ p/q/5.json p/q/5.md"; got != want {
		t.Errorf("files %s, want %s", got, want)
	}
	if got := f.read("tasks/p/q/5.md"); got != "notes\n" {
		t.Errorf(".md %q", got)
	}
}

func TestMoveCases(t *testing.T) {
	short := func(folder, from, created string, changed bool) string {
		return fmt.Sprintf(`"folder":"%s","notes_path":"~/tasks%s5.md","from":"%s","created":[%s],"changed":%t}`,
			folder, strings.TrimSuffix(folder, "/")+"/", from, created, changed)
	}
	for _, tc := range []struct {
		name, input string
		setup       func(f *fixture)
		want, files string
	}{
		{"into an existing folder", `{"id": 5, "to": "/p"}`, func(f *fixture) { f.task("", 5, false); f.write("tasks/p/.keep", "") },
			short("/p", "/", "", true), "p/ p/.keep p/5.json"},
		{"already there", `{"id": 5, "to": "/p"}`, func(f *fixture) { f.task("p", 5, false); f.write("tasks/p/5.md", "n") },
			short("/p", "/p", "", false), "p/ p/5.json p/5.md"},
		{"an empty stray .md is replaced by the notes", `{"id": 5, "to": "/p"}`, func(f *fixture) {
			f.task("", 5, false)
			f.write("tasks/5.md", "mine")
			f.write("tasks/p/5.md", "")
		}, short("/p", "/", "", true), "p/ p/5.json p/5.md"},
		{"an empty stray .md is removed when there are no notes", `{"id": 5, "to": "/p"}`, func(f *fixture) {
			f.task("", 5, false)
			f.write("tasks/p/5.md", "")
		}, short("/p", "/", "", true), "p/ p/5.json"},
		{"a stray .md with text is a conflict", `{"id": 5, "to": "/p"}`, func(f *fixture) {
			f.task("", 5, false)
			f.write("tasks/5.md", "mine")
			f.write("tasks/p/5.md", "edits")
		}, `conflict {"rule":"destination-exists","ids":[5]}`, "5.json 5.md p/ p/5.md"},
		{"a stray .md with text is a conflict when there are no notes", `{"id": 5, "to": "/p"}`, func(f *fixture) {
			f.task("", 5, false)
			f.write("tasks/p/5.md", "edits")
		}, `conflict {"rule":"destination-exists","ids":[5]}`, "5.json p/ p/5.md"},
		{"blockers and dependents are not read", `{"id": 5, "to": "/p"}`, func(f *fixture) {
			f.task("", 5, false, 6)
			f.write("tasks/6.json", "{")
			f.write("tasks/p/.keep", "")
		}, "", "6.json p/ p/.keep p/5.json"},

		{"invalid", `{"id": 5, "to": "p"}`, nil, `invalid-input {"problems":[{"field":"/to","reason":"must be a folder path from the root, like / or /proj/travel, each segment must be 1-64 lowercase letters, digits, and hyphens, not starting or ending with a hyphen"}]}`, ""},
		{"folder not found", `{"id": 5, "to": "/p/q"}`, func(f *fixture) { f.task("", 5, false) },
			`not-found {"folders":["/p"],"ids":[],"paths":[]}`, "5.json"},
		{"folder and task not found", `{"id": 5, "to": "/p"}`, nil,
			`not-found {"folders":["/p"],"ids":[5],"paths":[]}`, ""},
		{"not-found before an unusable task", `{"id": 5, "to": "/p"}`, func(f *fixture) { f.write("tasks/5.json", "{") },
			`not-found {"folders":["/p"],"ids":[],"paths":[]}`, "5.json"},
		{"unusable", `{"id": 5, "to": "/p"}`, func(f *fixture) { f.write("tasks/5.json", "{"); f.write("tasks/p/.keep", "") },
			`corrupt {"path":"~/tasks/5.json","reason":"not-json","detail":"not valid JSON: unexpected end of input"}`, "5.json p/ p/.keep"},
		{"to is a file", `{"id": 5, "to": "/p"}`, func(f *fixture) { f.task("", 5, false); f.write("tasks/p", "") },
			`corrupt {"path":"~/tasks/p","reason":"unexpected-file"}`, "5.json p"},
		{"duplicated", `{"id": 5, "to": "/p"}`, func(f *fixture) { f.task("", 5, false); f.task("p", 5, false) },
			`conflict {"rule":"duplicate-id","ids":[5]}`, "5.json p/ p/5.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			if tc.setup != nil {
				tc.setup(f)
			}
			got := f.op("move", tc.input)
			if !strings.HasSuffix(got, tc.want) {
				t.Errorf("got  %s\nwant …%s", got, tc.want)
			}
			if got := f.files(); got != tc.files {
				t.Errorf("files %q, want %q", got, tc.files)
			}
		})
	}
}

// A .md in to that is already a second name for the notes — an interrupted
// move's link — is taken as the notes, and the temp link leaves nothing.
func TestMoveOntoItsLink(t *testing.T) {
	f := newFixture(t)
	f.task("", 5, false)
	f.write("tasks/5.md", "mine")
	f.mkdir("tasks/p")
	f.link("tasks/5.md", "tasks/p/5.md")
	f.op("move", `{"id": 5, "to": "/p"}`)
	if got := f.entries(); got != "p/ p/5.json p/5.md" {
		t.Errorf("entries %q", got)
	}
	if got := f.read("tasks/p/5.md"); got != "mine" {
		t.Errorf("notes %q", got)
	}
}

// The notes' link is flushed with its folder before the task file is
// renamed, so a system crash can't keep the rename and the old .md's
// removal but lose the link (implementation-spec.md, Notes move by hard
// link).
func TestMoveFlushesNotesFirst(t *testing.T) {
	f := newFixture(t)
	f.task("", 5, false)
	f.write("tasks/5.md", "notes\n")
	f.mkdir("tasks/p")
	var steps []string
	f.hook(func(o fsys.Op) error {
		switch o.Name {
		case fsys.OpLink, fsys.OpRename, fsys.OpRenameNR:
			steps = append(steps, o.Name+" "+o.NewPath)
		case fsys.OpSyncDir, fsys.OpRemove:
			steps = append(steps, o.Name+" "+o.Path)
		}
		return nil
	})
	f.op("move", `{"id": 5, "to": "/p"}`)
	got := regexp.MustCompile(regexp.QuoteMeta(fsys.TempPrefix)+`[0-9a-f]+`).ReplaceAllString(strings.Join(steps, "; "), "TMP")
	if want := "link p/TMP; rename p/5.md; syncdir p; remove p/TMP; rename-noreplace p/5.json; remove 5.md"; got != want {
		t.Errorf("steps %q\nwant  %q", got, want)
	}
}

func TestMoveFolderCases(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		setup       func(f *fixture)
		want, files string
	}{
		{"into an existing folder", `{"folder": "/p/a", "to": "/q"}`, func(f *fixture) { f.task("p/a", 5, false); f.write("tasks/q/.keep", "") },
			`{"folder":"/q/a","from":"/p/a","created":[],"changed":true}`, "p/ q/ q/.keep q/a/ q/a/5.json"},
		{"into the root", `{"folder": "/p/a", "to": "/"}`, func(f *fixture) { f.task("p/a", 5, false) },
			`{"folder":"/a","from":"/p/a","created":[],"changed":true}`, "a/ a/5.json p/"},
		{"renamed", `{"folder": "/p/a", "to": "/p/b"}`, func(f *fixture) { f.task("p/a", 5, false) },
			`{"folder":"/p/b","from":"/p/a","created":[],"changed":true}`, "p/ p/b/ p/b/5.json"},
		{"parents", `{"folder": "/p", "to": "/x/y/z", "parents": true}`, func(f *fixture) { f.task("p", 5, false) },
			`{"folder":"/x/y/z","from":"/p","created":["/x","/x/y"],"changed":true}`, "x/ x/y/ x/y/z/ x/y/z/5.json"},
		{"already there", `{"folder": "/p/a", "to": "/p"}`, func(f *fixture) { f.task("p/a", 5, false) },
			`{"folder":"/p/a","from":"/p/a","created":[],"changed":false}`, "p/ p/a/ p/a/5.json"},

		{"the root", `{"folder": "/", "to": "/p"}`, nil, `invalid-input {"problems":[{"field":"/folder","reason":"the root can never be moved"}]}`, ""},
		{"into itself", `{"folder": "/p", "to": "/p/a"}`, nil, `invalid-input {"problems":[{"field":"/to","reason":"a folder can't be moved into itself"}]}`, ""},
		{"onto itself", `{"folder": "/p", "to": "/p"}`, nil, `invalid-input {"problems":[{"field":"/to","reason":"a folder can't be moved into itself"}]}`, ""},
		{"a missing folder above to", `{"folder": "/p", "to": "/x/y"}`, func(f *fixture) { f.write("tasks/p/.keep", "") },
			`not-found {"folders":["/x"],"ids":[],"paths":[]}`, "p/ p/.keep"},
		{"both missing", `{"folder": "/p", "to": "/x/y"}`, nil,
			`not-found {"folders":["/p","/x"],"ids":[],"paths":[]}`, ""},
		{"a folder is there", `{"folder": "/p/a", "to": "/q"}`, func(f *fixture) { f.write("tasks/p/a/.keep", ""); f.write("tasks/q/a/.keep", "") },
			`conflict {"rule":"destination-exists","ids":[]}`, "p/ p/a/ p/a/.keep q/ q/a/ q/a/.keep"},
		{"an empty folder is there: never replaced", `{"folder": "/p", "to": "/q"}`, func(f *fixture) {
			f.write("tasks/p/.keep", "")
			f.write("tasks/q/.keep", "")
			f.remove("tasks/q/.keep")
		}, `{"folder":"/q/p","from":"/p","created":[],"changed":true}`, "q/ q/p/ q/p/.keep"},
		{"a file is there", `{"folder": "/p", "to": "/q"}`, func(f *fixture) { f.write("tasks/p/.keep", ""); f.write("tasks/q/p", "") },
			`conflict {"rule":"destination-exists","ids":[]}`, "p/ p/.keep q/ q/p"},
		{"to is a file", `{"folder": "/p", "to": "/q"}`, func(f *fixture) { f.write("tasks/p/.keep", ""); f.write("tasks/q", "") },
			`corrupt {"path":"~/tasks/q","reason":"unexpected-file"}`, "p/ p/.keep q"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			if tc.setup != nil {
				tc.setup(f)
			}
			if got := f.op("move-folder", tc.input); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
			if got := f.files(); got != tc.files {
				t.Errorf("files %q, want %q", got, tc.files)
			}
		})
	}
}
