package main_test

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/testrun"
)

// The model's create, block, unblock, complete, reopen, claim, claim-next,
// release, move, delete and archive, of tasks and of folders, and batch, run beside koan on
// the same inputs, agree on every result and refusal, and on readiness,
// edges, claims, the frontier and the folders after each.
func TestEdgesModel(t *testing.T) {
	// The first seed alone makes every command succeed, for -short.
	seeds := []int64{11, 1, 2}
	if testing.Short() {
		seeds = seeds[:1]
	}
	// Every command must succeed somewhere in the sequences run.
	succeeded := map[string]int{}
	for _, seed := range seeds {
		t.Run(fmt.Sprint(seed), func(t *testing.T) { edgesModel(t, seed, succeeded) })
	}
	for _, cmd := range []string{"task create", "task block", "task unblock", "task complete", "task reopen", "task claim",
		"task claim-next", "task release", "task move", "task delete", "task archive", "folder create", "folder move",
		"folder delete", "folder archive", "batch"} {
		if succeeded[cmd] == 0 {
			t.Errorf("no %s succeeded", cmd)
		}
	}
}

func edgesModel(t *testing.T, seed int64, succeeded map[string]int) {
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
	edgeList := func() []string {
		var out []string
		for range rng.Intn(3) {
			out = append(out, edgeRef())
		}
		return out
	}
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
			out = append(out, fmt.Sprintf("%v %v %v %v", g["id"], f["id"], f["path"], g["end"]))
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

	var made, refused int
	for step := range 160 {
		var args []string
		var stdin string
		var err error
		var want string
		op := -1
		if brng.Intn(8) > 0 {
			op = rng.Intn(20)
		}
		switch {
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
			args = edgeArgs([]string{"task", "block", x}, bb, bl)
			want = fmt.Sprintf("%d %v %s %v %s", out.ID, out.Changed, gates(out.GatesMade), nonNil(out.Reblocked), warnings(out.Warnings))
		case op < 8:
			x, bb, bl := subject(), edgeList(), edgeList()
			var out model.EdgeResult
			out, err = m.Unblock(x, bb, bl)
			args = edgeArgs([]string{"task", "unblock", x}, bb, bl)
			want = fmt.Sprintf("%d %v %v", out.ID, out.Changed, nonNil(out.Released))
		case op == 13:
			x, to := subject(), folderRef()
			var changed bool
			var ws []model.Warning
			changed, ws, err = m.MoveTask(x, to)
			args = []string{"task", "move", x, "--to", to}
			want = fmt.Sprintf("%v %s", changed, warnings(ws))
		case op == 14:
			x := subject()
			var out model.Removed
			out, err = m.DeleteTask(x)
			args = []string{"task", "delete", x}
			want = fmt.Sprintf("%v %v", nonNil(out.Removed), nonNil(out.Released))
		case op >= 18:
			var left []int64
			if op == 18 {
				x := subject()
				left, err = m.ArchiveTask(x)
				args = []string{"task", "archive", x}
			} else {
				x := folderRef()
				left, err = m.ArchiveFolder(x)
				args = []string{"folder", "archive", x}
			}
			want = fmt.Sprint(nonNil(left))
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
				var out model.FolderMoved
				out, err = m.MoveFolder(x, to)
				args = []string{"folder", "move", x, "--to", to}
				want = fmt.Sprintf("%d %v %s %v %s", out.ID, out.Changed, out.Path, madeAt(out.Created), warnings(out.Warnings))
			default:
				contents := rng.Intn(3) == 0
				var out model.Removed
				out, err = m.DeleteFolder(x, contents, false)
				args = []string{"folder", "delete", x, fmt.Sprintf("--contents=%v", contents)}
				want = fmt.Sprintf("%v %v", nonNil(out.Removed), nonNil(out.Released))
			}
		default:
			x := subject()
			switch rng.Intn(5) {
			case 0:
				var out model.CompleteResult
				out, err = m.Complete(x)
				args = []string{"task", "complete", x}
				want = fmt.Sprintf("%d %v %v", out.ID, out.Changed, nonNil(out.Released))
			case 1:
				var out model.CompleteResult
				out, err = m.Reopen(x)
				args = []string{"task", "reopen", x}
				want = fmt.Sprintf("%d %v %v %s", out.ID, out.Changed, nonNil(out.Reblocked), warnings(out.Warnings))
			case 2:
				var c model.Claim
				c, err = m.ClaimTask(x, nil)
				args = []string{"task", "claim", x}
				want = fmt.Sprint(c.Task)
			case 3:
				var changed bool
				changed, err = m.Release(x)
				args = []string{"task", "release", x}
				want = fmt.Sprint(changed)
			default:
				sc := model.Scope{Folder: pick([]string{"/", "/a", "/a/b", "/c"}), Recursive: rng.Intn(2) == 0}
				var c *model.Claim
				c, err = m.ClaimNext(sc, nil)
				args = []string{"task", "claim-next", "--folder", sc.Folder, fmt.Sprintf("--recursive=%v", sc.Recursive)}
				want = "<nil>"
				if c != nil {
					want = fmt.Sprint(c.Task)
				}
			}
		}
		if args != nil {
			name := args[0] + " " + args[1]
			var r *testrun.Result
			if stdin != "" {
				name = args[0]
				r = input(t, home, name, stdin)
			} else {
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
				switch args[1] {
				case "-i":
					got = fmt.Sprintf("%v %v %v %s %v %s", koanMade(res["folders"]), res["tasks"], res["labels"],
						koanGates(r), res["reblocked"], koanWarnings(r))
				case "create":
					if args[0] == "folder" {
						got = fmt.Sprint(koanMade(res["created"]))
						break
					}
					got = fmt.Sprintf("%v %s %v %s", res["id"], koanGates(r), res["reblocked"], koanWarnings(r))
				case "block":
					got = fmt.Sprintf("%v %v %s %v %s", res["id"], res["changed"], koanGates(r), res["reblocked"], koanWarnings(r))
				case "unblock", "complete":
					got = fmt.Sprintf("%v %v %v", res["id"], res["changed"], res["released"])
				case "reopen":
					got = fmt.Sprintf("%v %v %v %s", res["id"], res["changed"], res["reblocked"], koanWarnings(r))
				case "claim":
					got = fmt.Sprint(res["claim"].(map[string]any)["task"])
				case "release":
					got = fmt.Sprint(res["changed"])
				case "claim-next":
					got = "<nil>"
					if c, _ := res["claim"].(map[string]any); c != nil {
						got = fmt.Sprint(c["task"])
					}
				case "move":
					if args[0] == "task" {
						got = fmt.Sprintf("%v %s", res["changed"], koanWarnings(r))
					} else {
						got = fmt.Sprintf("%v %v %v %v %s", res["id"], res["changed"], res["path"], koanMade(res["created"]), koanWarnings(r))
					}
				case "delete":
					got = fmt.Sprintf("%v %v", res["removed"], res["released"])
				case "archive":
					got = fmt.Sprint(res["left"])
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
			wantTasks = append(wantTasks, fmt.Sprintf("%d %s %v %v", id, readiness, nonNil(bb), claimed))
		}
		for _, x := range ok(t, home, "task", "list", "--state", "open,complete", "--limit", "null", "--fields", "readiness,blocked_by,claim").Result()["tasks"].([]any) {
			x := x.(map[string]any)
			var bb []any
			for _, e := range x["blocked_by"].([]any) {
				bb = append(bb, e.(map[string]any)["id"])
			}
			if bb == nil {
				bb = []any{}
			}
			gotTasks = append(gotTasks, fmt.Sprintf("%v %v %v %v", x["id"], x["readiness"], bb, x["claim"] != nil))
		}
		if !slices.Equal(gotTasks, wantTasks) {
			t.Fatalf("step %d: tasks: koan %v, model %v", step, gotTasks, wantTasks)
		}
		var wantFolders, gotFolders []string
		for _, id := range m.TreeOrder() {
			if _, isFolder := m.Folders[id]; isFolder {
				wantFolders = append(wantFolders, fmt.Sprintf("%d %s", id, m.Path(id)))
			}
		}
		for _, f := range ok(t, home, "folder", "list", "--limit", "null", "--fields", "path").Result()["folders"].([]any) {
			f := f.(map[string]any)
			gotFolders = append(gotFolders, fmt.Sprintf("%v %v", f["id"], f["path"]))
		}
		if !slices.Equal(gotFolders, wantFolders) {
			t.Fatalf("step %d: folders: koan %v, model %v", step, gotFolders, wantFolders)
		}
		var front []string
		for _, x := range ok(t, home, "task", "frontier", "--limit", "null", "--fields", "id").Result()["tasks"].([]any) {
			front = append(front, fmt.Sprint(x.(map[string]any)["id"]))
		}
		if fmt.Sprint(front) != fmt.Sprint(m.Frontier()) {
			t.Fatalf("step %d: frontier: koan %v, model %v", step, front, m.Frontier())
		}
		// The tree and the rows agree after every command.
		if r := ok(t, home, "doctor"); len(r.Result()["findings"].([]any)) != 0 {
			t.Fatalf("step %d %q: doctor: %s", step, args, r.Line())
		}
	}
	// The sequence must try both sides.
	if made < 5 || refused < 5 {
		t.Errorf("%d changes made, %d refused", made, refused)
	}
	t.Logf("%d changes made, %d refused: %v", made, refused, succeeded)
}

