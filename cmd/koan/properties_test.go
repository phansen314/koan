package main_test

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/testrun"
)

var (
	seedFlag  = flag.Int64("seed", 0, "run the property test with this seed alone")
	stepsFlag = flag.Int("steps", 0, "how many commands each property run makes; 0 is the default")
)

// Invariants as properties, with the reference model: random sequences of
// every write command, of tasks and of folders together, run against a
// fresh root and beside the model on the same inputs. koan and the model
// agree on every result and refusal, and on readiness, edges, claims,
// fields, the frontier and the folders after each; doctor finds nothing,
// and I1 to I12 and T1 to T6 hold, read directly from the database and
// the disk. A failure prints its seed and the commands run; -seed N runs
// that seed alone. -short runs the fixed seeds; the full run adds more,
// each longer, from a seed of the moment, which it prints.
func TestProperties(t *testing.T) {
	t.Parallel()
	// These seeds together make every command succeed, for -short.
	seeds := []int64{1, 59, 11, 2}
	steps := 200
	switch {
	case *seedFlag != 0:
		seeds = []int64{*seedFlag}
	case testing.Short():
	default:
		steps = 1000
		now := time.Now().UnixNano()
		t.Logf("seeds from %d", now)
		for i := range int64(9) {
			seeds = append(seeds, now+i)
		}
	}
	if *stepsFlag != 0 {
		steps = *stepsFlag
	}
	// Every command must succeed somewhere in the sequences run.
	var mu sync.Mutex
	succeeded := map[string]int{}
	t.Run("seeds", func(t *testing.T) {
		for _, seed := range seeds {
			t.Run(fmt.Sprint(seed), func(t *testing.T) {
				t.Parallel()
				got := properties(t, seed, steps)
				mu.Lock()
				defer mu.Unlock()
				for k, n := range got {
					succeeded[k] += n
				}
			})
		}
	})
	if t.Failed() || *seedFlag != 0 {
		return
	}
	for _, cmd := range []string{"task create", "update task", "update folder", "block", "unblock", "complete", "reopen",
		"claim", "claim-next", "unclaim", "mv task", "delete task", "archive task", "folder create",
		"mv folder", "mv case", "delete folder", "delete material", "archive folder", "batch"} {
		if succeeded[cmd] == 0 {
			t.Errorf("no %s succeeded", cmd)
		}
	}
}

func properties(t *testing.T, seed int64, steps int) map[string]int {
	succeeded := map[string]int{}
	rng := rand.New(rand.NewSource(seed))
	// Batches draw from a source of their own, so that the other changes
	// draw as they would without them.
	brng := rand.New(rand.NewSource(seed))
	home := root(t)
	m := model.New(1, func() string { return "2026-10-20T10:00:00Z" })
	for _, p := range []string{"/a/b", "/c"} {
		if _, err := m.CreateFolder(p, nil, nil); err != nil {
			t.Fatal(err)
		}
		ok(t, home, "folder", "create", p)
	}
	taskIDs := func() []int64 {
		var out []int64
		for id := range m.Tasks {
			out = append(out, id)
		}
		slices.Sort(out)
		return out
	}
	pick := func(pool []string) string { return pool[rng.Intn(len(pool))] }
	edgeRef := func() string {
		if ids := taskIDs(); len(ids) > 0 && rng.Intn(3) > 0 {
			return fmt.Sprint(ids[rng.Intn(len(ids))])
		}
		if rng.Intn(5) == 0 {
			return pick([]string{"/A", "@99", "/a/x.start"})
		}
		return pick([]string{"/a", "/a.start", "/a/b.done", "@2.start", "@3", "/c.start", "@1/b"})
	}
	subject := func() string {
		if ids := taskIDs(); len(ids) > 0 && rng.Intn(3) > 0 {
			return fmt.Sprint(ids[rng.Intn(len(ids))])
		}
		if rng.Intn(4) == 0 {
			return pick([]string{"@1", "99", "/a/B.done"})
		}
		return pick([]string{"/a.start", "/c.done", "@2.start"})
	}
	// subjects is a list of one to three references, the first x and the
	// rest from more, now and then with one given twice, and the words
	// that give it: each its own word, or joined to the one before by a
	// comma.
	subjects := func(x string, more func() string) (xs, words []string) {
		xs = []string{x}
		for range rng.Intn(3) {
			if rng.Intn(6) == 0 {
				xs = append(xs, xs[rng.Intn(len(xs))])
			} else {
				xs = append(xs, more())
			}
		}
		for i, x := range xs {
			if i > 0 && rng.Intn(2) == 0 {
				words[len(words)-1] += "," + x
			} else {
				words = append(words, x)
			}
		}
		return xs, words
	}
	edgeList := func() []string {
		var out []string
		for range rng.Intn(3) {
			out = append(out, edgeRef())
		}
		return out
	}
	// On task create, block and unblock, what it waits on is --blocked-by.
	edgeArgs := func(args []string, bb, bl []string) []string {
		if len(bb) > 0 {
			args = append(args, "--blocked-by", strings.Join(bb, ","))
		}
		if len(bl) > 0 {
			args = append(args, "--blocks", strings.Join(bl, ","))
		}
		return args
	}
	// sameErr reports whether koan refused as the model did.
	sameErr := func(r *testrun.Result, err error) bool {
		e := err.(*core.Error)
		got := r.Error()
		if got == nil {
			return false
		}
		rule, _ := got["rule"].(string)
		var problems []string
		for _, p := range got["problems"].([]any) {
			problems = append(problems, p.(map[string]any)["field"].(string))
		}
		var archived []any
		for _, a := range got["archived"].([]any) {
			archived = append(archived, a.(map[string]any)["id"])
		}
		return r.Kind() == e.Kind && rule == e.Rule && fmt.Sprint(got["ids"]) == fmt.Sprint(nonNil(e.IDs)) &&
			fmt.Sprint(archived) == fmt.Sprint(e.Archived) &&
			fmt.Sprint(got["paths"]) == fmt.Sprint(nonNilS(e.Paths)) && fmt.Sprint(problems) == fmt.Sprint(e.Problems)
	}
	gates := func(made []model.GateMade) string {
		var out []string
		for _, g := range made {
			out = append(out, fmt.Sprintf("%d %d %s %s", g.ID, g.Folder, g.Path, g.End))
		}
		return fmt.Sprint(out)
	}
	koanGates := func(r *testrun.Result) string {
		var out []string
		for _, g := range r.Result()["gates_made"].([]any) {
			g := g.(map[string]any)
			f := g["folder"].(map[string]any)
			out = append(out, fmt.Sprintf("%v %v %v %v", g["id"], f["id"], f["path"], g["gate"]))
		}
		return fmt.Sprint(out)
	}
	madeAt := func(made []model.FolderAt) []string {
		out := []string{}
		for _, f := range made {
			out = append(out, fmt.Sprintf("%d %s", f.ID, f.Path))
		}
		return out
	}
	// pick2 is one of two, at even odds.
	pick2 := func(a, b func() string) func() string {
		if rng.Intn(2) == 0 {
			return a
		}
		return b
	}
	movedAt := func(moved []model.FolderAt) []string {
		out := []string{}
		for _, f := range moved {
			out = append(out, core.Kept(f.ID, f.Path, core.EndNone))
		}
		return out
	}
	koanMoved := func(v any) []string {
		out := []string{}
		for _, f := range v.([]any) {
			out = append(out, f.(map[string]any)["ref"].(string))
		}
		return out
	}
	koanMade := func(v any) []string {
		out := []string{}
		for _, f := range v.([]any) {
			f := f.(map[string]any)
			out = append(out, fmt.Sprintf("%v %v", f["id"], f["path"]))
		}
		return out
	}
	// folderRef is mostly a folder there is, by path or ID; else one from
	// a pool that may name nothing, or the root.
	folderRef := func() string {
		var live []int64
		for _, id := range m.TreeOrder() {
			if _, ok := m.Folders[id]; ok {
				live = append(live, id)
			}
		}
		if len(live) > 0 && rng.Intn(3) > 0 {
			id := live[rng.Intn(len(live))]
			if rng.Intn(2) == 0 {
				return fmt.Sprint(id)
			}
			return m.Path(id)
		}
		return pick([]string{"/a", "/a/b", "/c", "/d", "@1", "@2", "/c/e", "/A", "/"})
	}
	folderPaths := []string{"/d", "/a/b/x", "/c/a", "/x/y", "/C", "/a/B", "/c", "/a", "/c/e", "/a/b"}
	warnings := func(ws []model.Warning) string {
		var out []string
		for _, w := range ws {
			out = append(out, fmt.Sprintf("%s %v", w.Kind, w.IDs))
		}
		return fmt.Sprint(out)
	}
	koanWarnings := func(r *testrun.Result) string {
		var out []string
		for _, w := range r.Envelope["warnings"].([]any) {
			w := w.(map[string]any)
			out = append(out, fmt.Sprintf("%s %v", w["kind"], w["ids"]))
		}
		return fmt.Sprint(out)
	}

	// batchRef is an edge-ref, or often a label of one of a batch's n
	// tasks, now and then one no part defines.
	batchRef := func(n int) string {
		switch rng.Intn(8) {
		case 0, 1, 2:
			return "$" + string(rune('a'+rng.Intn(n)))
		case 3:
			return "$z"
		}
		return edgeRef()
	}
	// batchIn is a batch file, as the model and as JSON.
	batchIn := func() (model.BatchInput, string) {
		var in model.BatchInput
		file := map[string]any{}
		var folders, tasks []any
		for range []int{0, 0, 1, 2}[rng.Intn(4)] {
			p := pick(folderPaths)
			in.Folders = append(in.Folders, model.BatchFolder{Path: p})
			folders = append(folders, map[string]any{"path": p})
		}
		if rng.Intn(3) == 0 {
			in.Folder = folderRef()
			file["folder"] = in.Folder
		}
		n := rng.Intn(4)
		if rng.Intn(5) > 0 {
			n = max(n, 1)
		}
		for range n {
			x := model.BatchTask{TaskInput: model.TaskInput{Title: "t"}}
			part := map[string]any{"title": "t"}
			// Each label once, now and then twice.
			if l := "$" + string(rune('a'+len(in.Tasks))); rng.Intn(4) > 0 {
				if rng.Intn(10) == 0 {
					l = "$a"
				}
				x.Label = l
				part["label"] = x.Label
			}
			if rng.Intn(3) == 0 {
				x.Folder = folderRef()
				if len(in.Folders) > 0 && rng.Intn(2) == 0 {
					x.Folder = in.Folders[0].Path
				}
				part["folder"] = x.Folder
			}
			for range rng.Intn(3) {
				x.BlockedBy = append(x.BlockedBy, batchRef(n))
			}
			for range rng.Intn(2) {
				x.Blocks = append(x.Blocks, batchRef(n))
			}
			if x.BlockedBy != nil {
				part["blocked_by"] = x.BlockedBy
			}
			if x.Blocks != nil {
				part["blocks"] = x.Blocks
			}
			in.Tasks = append(in.Tasks, x)
			tasks = append(tasks, part)
		}
		if folders != nil {
			file["folders"] = folders
		}
		if tasks != nil {
			file["tasks"] = tasks
		}
		b, _ := json.Marshal(file)
		return in, string(b)
	}

	var sequence []string
	// The rest, updates, case-only moves, forced deletes and hand edits,
	// draw from a source of their own too.
	xrng := rand.New(rand.NewSource(seed + 1))
	// dir is a folder's directory on disk, the root's for the root.
	dir := func(id int64) string {
		p := filepath.Join(home, "koan-root", "tree")
		if id != core.RootID {
			p = filepath.Join(p, strings.ReplaceAll(m.Path(id), "/", "/@"))
		}
		return p
	}
	liveFolders := func() []int64 {
		var out []int64
		for _, id := range m.TreeOrder() {
			if _, ok := m.Folders[id]; ok {
				out = append(out, id)
			}
		}
		return out
	}
	// update is a random update, as the model and as options.
	update := func(folder bool) (model.Update, []string) {
		var u model.Update
		var args []string
		for len(args) == 0 {
			if rng.Intn(2) == 0 {
				title := pick([]string{"t", "u", " v ", "t", "null"})
				u.SetTitle, u.Title = true, &title
				if title == "null" {
					u.Title = nil
				}
				args = append(args, "--title", title)
			}
			if !folder && rng.Intn(3) == 0 {
				u.SetPriority = true
				if p := int64(rng.Intn(3)); p > 0 {
					u.Priority = &p
					args = append(args, "--priority", fmt.Sprint(p))
				} else {
					args = append(args, "--priority", "null")
				}
			}
			if !folder && rng.Intn(3) == 0 {
				switch tags := []string{pick([]string{"a", "b"}), pick([]string{"c", "a"})}; rng.Intn(3) {
				case 0:
					u.TagsAdd = slices.Compact(tags)
					args = append(args, "--tags-add", strings.Join(u.TagsAdd, ","))
				case 1:
					u.TagsRemove = tags[:1]
					args = append(args, "--tags-remove", tags[0])
				default:
					u.TagsReplaceAll = tags[1:]
					args = append(args, "--tags-replace-all", tags[1])
				}
			}
			if rng.Intn(3) == 0 {
				k := pick([]string{"status", "n"})
				if rng.Intn(3) == 0 {
					u.ExtraRemove = []string{k}
					args = append(args, "--extra-remove", k)
				} else {
					v := pick([]string{"todo", "done"})
					u.ExtraMerge = map[string]any{k: v}
					args = append(args, "--extra-merge", fmt.Sprintf(`{%q:%q}`, k, v))
				}
			}
		}
		return u, args
	}
	// extraOp is one of the rest: it returns the command run, and the
	// name it counts under, or no command for a hand edit.
	extraOp := func() (args []string, name, want string, err error) {
		folders := liveFolders()
		switch op := rng.Intn(10); {
		case op < 5:
			// Tasks, folders or both, now and then with a field one of
			// them doesn't have.
			x, more, name := subject(), subject, "update task"
			if op >= 3 {
				x, more, name = folderRef(), folderRef, "update folder"
			}
			if rng.Intn(4) == 0 {
				more = pick2(subject, folderRef)
			}
			xs, words := subjects(x, more)
			u, opts := update(op >= 3 && rng.Intn(5) > 0)
			var updated []int64
			updated, err = m.Update(xs, u)
			return append(append([]string{"update"}, words...), opts...), name, fmt.Sprint(nonNil(updated)), err
		case op < 7 && len(folders) > 0:
			// A case-only move: one letter of the folder's own name
			// respelled, or now and then of a parent's, which names two
			// spellings.
			id := folders[rng.Intn(len(folders))]
			p := []byte(m.Path(id))
			i := strings.LastIndex(string(p), "/") + 1
			if rng.Intn(5) == 0 {
				i = 1
			}
			for ; i < len(p) && !('a' <= p[i]|0x20 && p[i]|0x20 <= 'z'); i++ {
			}
			if i == len(p) || p[i] == '/' {
				return nil, "", "", nil
			}
			p[i] ^= 0x20
			x := fmt.Sprint(id)
			if rng.Intn(2) == 0 {
				x = m.Path(id)
			}
			mv, args := model.MvDest, []string{"mv", x, string(p)}
			if rng.Intn(2) == 0 {
				mv, args = model.MvRename, []string{"mv", "-T", x, string(p)}
			}
			var out model.MoveResult
			out, err = m.Move(mv, string(p), x)
			return args, "mv case", fmt.Sprintf("%v %s", movedAt(out.Moved), warnings(out.Warnings)), err
		case op == 7:
			xs, words := subjects(folderRef(), pick2(folderRef, subject))
			var out model.Removed
			yes := true
			out, err = m.Delete(xs, &yes, &yes)
			return append(append([]string{"delete"}, words...), "--contents", "--material"), "delete material",
				fmt.Sprintf("%v %v %v %v", out.Deleted, nonNil(out.Removed), out.Unclaimed, nonNil(out.Released)), err
		case op == 8:
			// Material, by hand, in a folder or the root.
			id := core.RootID
			if len(folders) > 0 && rng.Intn(4) > 0 {
				id = folders[rng.Intn(len(folders))]
			}
			name := pick([]string{"docs", "main.go", ".git"})
			p := filepath.Join(dir(id), name)
			if name != "main.go" {
				os.MkdirAll(p, 0o755)
				p = filepath.Join(p, "x")
			}
			if e := os.WriteFile(p, []byte("material"), 0o644); e != nil {
				t.Fatal(e)
			}
			sequence = append(sequence, "by hand: material "+p)
			m.PutMaterial(id, name)
		default:
			// Notes, edited by hand.
			ids := taskIDs()
			if len(ids) == 0 {
				break
			}
			x := m.Tasks[ids[rng.Intn(len(ids))]]
			name := fmt.Sprintf("@%d.md", x.ID)
			if x.Gate != core.EndNone {
				name = "@" + string(x.Gate) + ".md"
			}
			p := filepath.Join(dir(x.Folder), name)
			if e := os.WriteFile(p, []byte("notes"), 0o644); e != nil {
				t.Fatal(e)
			}
			sequence = append(sequence, "by hand: notes "+p)
		}
		return nil, "", "", nil
	}

	// A failure prints the seed, and every command run to reach it.
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("seed %d; rerun with -run 'TestProperties/seeds/%d$' -seed %d -steps %d; the commands run:\n%s",
				seed, seed, seed, steps, strings.Join(sequence, "\n"))
		}
	})
	var prev *testrun.Snapshot

	var made, refused int
	for step := range steps {
		var args []string
		var stdin string
		var err error
		var want string
		var name string
		op := -1
		if xrng.Intn(5) == 0 {
			op = -2
		} else if brng.Intn(8) > 0 {
			op = rng.Intn(20)
		}
		switch {
		case op == -2:
			was := rng
			rng = xrng
			args, name, want, err = extraOp()
			rng = was
		case op == -1:
			was := rng
			rng = brng
			in, file := batchIn()
			rng = was
			var out model.BatchResult
			out, err = m.Batch(in)
			args, stdin = []string{"batch", "-i", "-"}, file
			want = fmt.Sprintf("%v %v %v %s %v %s", madeAt(out.Folders), nonNil(out.Tasks), out.Labels,
				gates(out.GatesMade), nonNil(out.Reblocked), warnings(out.Warnings))
		case op < 3:
			folder := pick([]string{"", "/a", "/a/b", "/c"})
			bb, bl := edgeList(), edgeList()
			var out model.TaskCreated
			out, err = m.CreateTask(model.TaskInput{Title: "t", Folder: folder, BlockedBy: bb, Blocks: bl})
			args = []string{"task", "create", "t"}
			if folder != "" {
				args = append(args, "--folder", folder)
			}
			args = edgeArgs(args, bb, bl)
			want = fmt.Sprintf("%d %s %v %s", out.ID, gates(out.GatesMade), nonNil(out.Reblocked), warnings(out.Warnings))
		case op < 6:
			x, bb, bl := subject(), edgeList(), edgeList()
			var out model.EdgeResult
			out, err = m.Block(x, bb, bl)
			args = edgeArgs([]string{"block", x}, bb, bl)
			want = fmt.Sprintf("%d %v %s %v %s", out.ID, out.Changed, gates(out.GatesMade), nonNil(out.Reblocked), warnings(out.Warnings))
		case op < 8:
			x, bb, bl := subject(), edgeList(), edgeList()
			var out model.EdgeResult
			out, err = m.Unblock(x, bb, bl)
			args = edgeArgs([]string{"unblock", x}, bb, bl)
			want = fmt.Sprintf("%d %v %v", out.ID, out.Changed, nonNil(out.Released))
		case op == 13:
			xs, words := subjects(subject(), pick2(subject, folderRef))
			into := folderRef()
			var out model.MoveResult
			mv := model.MvDest
			args = append(append([]string{"mv"}, words...), into)
			if rng.Intn(2) == 0 {
				mv, args = model.MvInto, append([]string{"mv", "-t", into}, words...)
			}
			out, err = m.Move(mv, into, xs...)
			name = "mv task"
			want = fmt.Sprintf("%v %s", movedAt(out.Moved), warnings(out.Warnings))
		case op == 14:
			xs, words := subjects(subject(), pick2(subject, folderRef))
			var out model.Removed
			out, err = m.Delete(xs, nil, nil)
			args = append([]string{"delete"}, words...)
			name = "delete task"
			want = fmt.Sprintf("%v %v %v %v", out.Deleted, nonNil(out.Removed), out.Unclaimed, nonNil(out.Released))
		case op >= 18:
			var archived, left []int64
			var xs, words []string
			if op == 18 {
				xs, words = subjects(subject(), pick2(subject, folderRef))
				name = "archive task"
			} else {
				xs, words = subjects(folderRef(), pick2(folderRef, subject))
				name = "archive folder"
			}
			// Each item archived is named by its kept text, with the
			// path it had.
			kept := map[int64]string{}
			for id, x := range m.Tasks {
				kept[id] = core.Kept(id, m.Path(x.Folder), x.Gate)
			}
			for id := range m.Folders {
				kept[id] = core.Kept(id, m.Path(id), core.EndNone)
			}
			archived, left, err = m.Archive(xs...)
			args = append([]string{"archive"}, words...)
			refs := []string{}
			for _, id := range archived {
				refs = append(refs, kept[id])
			}
			want = fmt.Sprintf("%v %v", refs, nonNil(left))
		case op >= 15:
			// Creates come most often and deletes least, so the folders
			// the other parts name come back.
			switch x, to := folderRef(), pick(folderPaths); rng.Intn(8) {
			case 0, 1, 2, 3:
				var made []model.FolderAt
				made, err = m.CreateFolder(to, nil, nil)
				args = []string{"folder", "create", to}
				want = fmt.Sprint(madeAt(made))
			case 4, 5, 6:
				// Into a folder, renamed, or as the destination says; now
				// and then the destination is any folder reference.
				if rng.Intn(4) == 0 {
					to = folderRef()
				}
				var out model.MoveResult
				switch xs, words := subjects(x, pick2(folderRef, subject)); rng.Intn(3) {
				case 0:
					out, err = m.Move(model.MvRename, to, x)
					args = []string{"mv", "-T", x, to}
				case 1:
					out, err = m.Move(model.MvInto, to, xs...)
					args = append([]string{"mv", "-t", to}, words...)
				default:
					out, err = m.Move(model.MvDest, to, xs...)
					args = append(append([]string{"mv"}, words...), to)
				}
				name = "mv folder"
				want = fmt.Sprintf("%v %s", movedAt(out.Moved), warnings(out.Warnings))
			default:
				contents := rng.Intn(3) == 0
				var out model.Removed
				xs, words := subjects(x, pick2(folderRef, subject))
				out, err = m.Delete(xs, &contents, nil)
				args = append(append([]string{"delete"}, words...), fmt.Sprintf("--contents=%v", contents))
				name = "delete folder"
				want = fmt.Sprintf("%v %v %v %v", out.Deleted, nonNil(out.Removed), out.Unclaimed, nonNil(out.Released))
			}
		default:
			x := subject()
			switch rng.Intn(5) {
			case 0:
				xs, words := subjects(x, subject)
				var out model.CompleteResult
				out, err = m.Complete(xs...)
				args = append([]string{"complete"}, words...)
				want = fmt.Sprintf("%v %v %v", nonNil(out.Changed), out.Unclaimed, nonNil(out.Released))
			case 1:
				xs, words := subjects(x, subject)
				var out model.CompleteResult
				out, err = m.Reopen(xs...)
				args = append([]string{"reopen"}, words...)
				want = fmt.Sprintf("%v %v %s", nonNil(out.Changed), nonNil(out.Reblocked), warnings(out.Warnings))
			case 2:
				xs, words := subjects(x, subject)
				var claimed []int64
				claimed, err = m.ClaimTask(xs, nil)
				args = append([]string{"claim"}, words...)
				want = fmt.Sprint(nonNil(claimed))
			case 3:
				xs, words := subjects(x, subject)
				var unclaimed []int64
				unclaimed, err = m.Unclaim(xs...)
				args = append([]string{"unclaim"}, words...)
				want = fmt.Sprint(nonNil(unclaimed))
			default:
				sc := model.Scope{Folder: pick([]string{"/", "/a", "/a/b", "/c"}), Direct: rng.Intn(2) == 0}
				var c *model.Claim
				c, err = m.ClaimNext(sc, nil)
				args = []string{"claim-next", sc.Folder, fmt.Sprintf("--direct=%v", sc.Direct)}
				want = "<nil>"
				if c != nil {
					want = fmt.Sprint(c.Task)
				}
			}
		}
		if args != nil {
			if name == "" {
				name = commandName(args)
			}
			var r *testrun.Result
			if stdin != "" {
				name = args[0]
				sequence = append(sequence, fmt.Sprintf("koan batch -i - <<< %s", stdin))
				r = input(t, home, name, stdin)
			} else {
				sequence = append(sequence, fmt.Sprintf("koan %q", args))
				r = koan(t, home, args...)
			}
			if err != nil {
				refused++
			} else {
				made++
				succeeded[name]++
			}
			switch {
			case err != nil:
				if !sameErr(r, err) {
					t.Fatalf("step %d %q: model %v %v, koan %s", step, args, err, err.(*core.Error).Problems, r.Line())
				}
			case !r.OK():
				t.Fatalf("step %d %q: model %s, koan %s", step, args, want, r.Line())
			default:
				res := r.Result()
				var got string
				verb := commandName(args)
				switch verb[strings.LastIndex(verb, " ")+1:] {
				case "batch":
					got = fmt.Sprintf("%v %v %v %s %v %s", koanMade(res["folders_made"]), res["tasks_made"], res["labels"],
						koanGates(r), res["reblocked"], koanWarnings(r))
				case "create":
					if args[0] == "folder" {
						got = fmt.Sprint(koanMade(res["folders_made"]))
						break
					}
					got = fmt.Sprintf("%v %s %v %s", res["id"], koanGates(r), res["reblocked"], koanWarnings(r))
				case "block":
					got = fmt.Sprintf("%v %v %s %v %s", res["id"], res["changed"], koanGates(r), res["reblocked"], koanWarnings(r))
				case "update":
					got = fmt.Sprint(res["updated"])
				case "unblock":
					got = fmt.Sprintf("%v %v %v", res["id"], res["changed"], res["released"])
				case "complete":
					got = fmt.Sprintf("%v %v %v", res["completed"], res["unclaimed"], res["released"])
				case "reopen":
					got = fmt.Sprintf("%v %v %s", res["reopened"], res["reblocked"], koanWarnings(r))
				case "claim":
					got = fmt.Sprint(res["claimed"])
				case "unclaim":
					got = fmt.Sprint(res["unclaimed"])
				case "claim-next":
					got = "<nil>"
					if x, _ := res["task"].(map[string]any); x != nil {
						got = fmt.Sprint(x["id"])
					}
				case "mv":
					got = fmt.Sprintf("%v %s", koanMoved(res["moved"]), koanWarnings(r))
				case "delete":
					got = fmt.Sprintf("%v %v %v %v", res["deleted"], res["removed"], res["unclaimed"], res["released"])
				case "archive":
					got = fmt.Sprintf("%v %v", koanMoved(res["archived"]), res["removed"])
				}
				if got != want {
					t.Fatalf("step %d %q: model %s, koan %s", step, args, want, got)
				}
			}
		}

		// Readiness and edges, task by task, then the frontier.
		var wantTasks, gotTasks []string
		for _, id := range m.TreeOrder() {
			x, isTask := m.Tasks[id]
			if !isTask {
				continue
			}
			readiness := "<nil>"
			if x.Open() {
				readiness = "blocked"
				if m.Ready(id) {
					readiness = "ready"
				}
			}
			var bb []int64
			for b := range x.BlockedBy {
				bb = append(bb, b)
			}
			slices.Sort(bb)
			_, claimed := m.Claims[id]
			p := "<nil>"
			if x.Priority != nil {
				p = fmt.Sprint(*x.Priority)
			}
			tags := []string{}
			for tag := range x.Tags {
				tags = append(tags, tag)
			}
			slices.Sort(tags)
			extra, _ := json.Marshal(x.Extra)
			wantTasks = append(wantTasks, fmt.Sprintf("%d %s %v %v %q %s %v %s", id, readiness, nonNil(bb), claimed, x.Title, p, tags, extra))
		}
		for _, x := range ok(t, home, "task", "list", "--state", "open,complete", "--limit", "null", "--fields", "readiness,blocked_by,claim,title,priority,tags,extra").Result()["tasks"].([]any) {
			x := x.(map[string]any)
			var bb []any
			for _, e := range x["blocked_by"].([]any) {
				bb = append(bb, e.(map[string]any)["id"])
			}
			if bb == nil {
				bb = []any{}
			}
			tags := []string{}
			for _, tag := range x["tags"].([]any) {
				tags = append(tags, tag.(string))
			}
			slices.Sort(tags)
			extra, _ := json.Marshal(x["extra"])
			gotTasks = append(gotTasks, fmt.Sprintf("%v %v %v %v %q %v %v %s", x["id"], x["readiness"], bb, x["claim"] != nil, x["title"], x["priority"], tags, extra))
		}
		if !slices.Equal(gotTasks, wantTasks) {
			t.Fatalf("step %d: tasks: koan %v, model %v", step, gotTasks, wantTasks)
		}
		var wantFolders, gotFolders []string
		for _, id := range m.TreeOrder() {
			if _, isFolder := m.Folders[id]; isFolder {
				f := m.Folders[id]
				title := "<nil>"
				if f.Title != nil {
					title = *f.Title
				}
				extra, _ := json.Marshal(f.Extra)
				wantFolders = append(wantFolders, fmt.Sprintf("%d %s %s %s", id, m.Path(id), title, extra))
			}
		}
		for _, f := range ok(t, home, "folder", "list", "--limit", "null", "--fields", "path,title,extra").Result()["folders"].([]any) {
			f := f.(map[string]any)
			extra, _ := json.Marshal(f["extra"])
			gotFolders = append(gotFolders, fmt.Sprintf("%v %v %v %s", f["id"], f["path"], f["title"], extra))
		}
		if !slices.Equal(gotFolders, wantFolders) {
			t.Fatalf("step %d: folders: koan %v, model %v", step, gotFolders, wantFolders)
		}
		var front []string
		for _, x := range ok(t, home, "frontier", "--limit", "null", "--fields", "id").Result()["tasks"].([]any) {
			front = append(front, fmt.Sprint(x.(map[string]any)["id"]))
		}
		if fmt.Sprint(front) != fmt.Sprint(m.Frontier()) {
			t.Fatalf("step %d: frontier: koan %v, model %v", step, front, m.Frontier())
		}
		// The tree and the rows agree after every command.
		if r := ok(t, home, "doctor"); len(r.Result()["findings"].([]any)) != 0 {
			t.Fatalf("step %d %q: doctor: %s", step, args, r.Line())
		}
		// And the invariants hold, read directly.
		var bad []string
		prev, bad = testrun.CheckRoot(home, prev, testrun.Checks{Notes: true})
		if len(bad) > 0 {
			t.Fatalf("step %d %q: invariants:\n%s", step, args, strings.Join(bad, "\n"))
		}
		if bad := m.Check(); len(bad) > 0 {
			t.Fatalf("step %d %q: the model's invariants:\n%s", step, args, strings.Join(bad, "\n"))
		}
	}
	// The sequence must try both sides.
	if made < 5 || refused < 5 {
		t.Errorf("%d changes made, %d refused", made, refused)
	}
	t.Logf("%d changes made, %d refused: %v", made, refused, succeeded)
	return succeeded
}

// The direct check finds each invariant broken by hand, in the database
// or on disk, and nothing on a root that holds them.
func TestCheckRoot(t *testing.T) {
	home := root(t)
	ok(t, home, "folder", "create", "/work/api") // 1, 2
	ok(t, home, "task", "create", "a", "--folder", "/work", "--blocked-by", "/work.start")
	ok(t, home, "task", "create", "b")
	ok(t, home, "complete", "5")
	ok(t, home, "archive", "5")
	ok(t, home, "task", "create", "c")
	snap, bad := testrun.CheckRoot(home, nil, testrun.Checks{Notes: true})
	if len(bad) > 0 {
		t.Fatalf("a sound root: %q", bad)
	}
	tree := treeDir(home)
	write(t, home, "@work/@todo.md", "x")
	write(t, home, "@notes.md", "")
	write(t, home, "@work/@6.md", "")
	write(t, home, "@work/@done.md", "")
	rename(t, home, "@work/@api", "@work/@API")
	os.Remove(filepath.Join(tree, "@6.md"))
	os.Remove(filepath.Join(home, "koan-root", "archived", "@5.md"))
	db(t, home,
		"INSERT INTO claim (task, claimed_at, extra) VALUES (5, '2026-10-20T10:00:00Z', '{}')",
		"INSERT INTO tag (task, tag) VALUES (3, 'Bad')",
		"INSERT INTO issued (id, kind, state) VALUES (7, 'task', 'live')",
		"UPDATE task SET created_at = '2026-10-20T11:00:00Z' WHERE id = 3",
		"INSERT INTO edge (task, blocker) VALUES (4, 3)",
		"DELETE FROM issued WHERE id = 6")
	_, bad = testrun.CheckRoot(home, snap, testrun.Checks{Notes: true})
	for _, want := range []string{
		"I1: @7 is issued live with no row", "I1: task @6 was never issued", "I2: @6 is no longer issued",
		"I7: cycle", "I8: claim on @5, which isn't a task", "I9: @3 tag \"Bad\"", "I9: @3 created_at changed",
		"I12: @5 archived alone", "T1: folder @2", "T2: ", "T4: ", "T5: ", "T6: " + filepath.Join(tree, "@work", "@todo.md"),
		"T6: " + filepath.Join(tree, "@work", "@done.md") + " is no gate's notes", "notes: @6 has no",
	} {
		if !slices.ContainsFunc(bad, func(b string) bool { return strings.HasPrefix(b, want) }) {
			t.Errorf("not found: %s", want)
		}
	}
	if t.Failed() {
		t.Logf("found:\n%s", strings.Join(bad, "\n"))
	}
}
