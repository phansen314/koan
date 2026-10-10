package ops

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/jsonio"
	"github.com/phansen314/koan/internal/model"
	"github.com/phansen314/koan/internal/store"
)

// batchMax is the most tasks one create-batch takes.
const batchMax = 1000

// CreateBatchInput is create-batch's input, defaults applied: each task's
// folder is its own or the batch's.
type CreateBatchInput struct {
	Tasks []BatchTask
}

// BatchTask is one task of a batch. Its blockers are existing tasks' IDs
// and earlier tasks' indexes in the batch, resolved from their refs.
type BatchTask struct {
	Ref       string // "" for none
	Title     model.Title
	Folder    model.FolderPath
	Priority  *int64
	Tags      []model.Tag
	BlockedBy []model.ID // existing tasks
	Earlier   []int      // earlier tasks in the batch, by index
	Extra     *jsonio.Object
	Notes     string
}

// blocker is one blocked_by item of a batch task: an existing task's ID, or
// an earlier task's ref.
type blocker struct {
	id  model.ID
	ref string
}

// indexed is a valid blocked_by item, with its index in blocked_by.
type indexed struct {
	at int
	blocker
}

func decodeCreateBatch(f *model.Fields, p *model.Problems) any {
	folder := optionalFolder(f, p, "folder")
	var in CreateBatchInput
	v, ok := f.Required("tasks")
	if !ok {
		return in
	}
	items, ok := p.Array(v, "/tasks")
	if !ok {
		return in
	}
	switch {
	case len(items) == 0:
		p.Add("/tasks", "must list at least one task")
	case len(items) > batchMax:
		p.Add("/tasks", fmt.Sprintf("must list at most %d tasks", batchMax))
	}
	refs := map[string]int{} // each valid ref, to the first task that has it
	for i, item := range items {
		ptr := jsonio.Pointer("/tasks", strconv.Itoa(i))
		t, blockers := decodeBatchTask(p, item, ptr, folder)
		if t.Ref != "" {
			if j, dup := refs[t.Ref]; dup {
				p.AddAdditional(ptr+"/ref", fmt.Sprintf("duplicate of task %d's ref", j))
			} else {
				refs[t.Ref] = i
			}
		}
		for _, b := range blockers {
			if b.ref == "" {
				t.BlockedBy = append(t.BlockedBy, b.id)
				continue
			}
			bptr := jsonio.Pointer(ptr+"/blocked_by", strconv.Itoa(b.at))
			// An earlier task first: a task whose ref duplicates an earlier
			// one's (already reported) is not named by that ref.
			j, found := refs[b.ref]
			switch {
			case found && j < i:
				t.Earlier = append(t.Earlier, j)
			case b.ref == t.Ref:
				p.AddAdditional(bptr, "names this task itself; a task cannot block itself")
			case laterRef(items, i, b.ref):
				p.AddAdditional(bptr, "names a later task in the batch; list a task's blockers before it")
			default:
				p.AddAdditional(bptr, "names no task in the batch")
			}
		}
		in.Tasks = append(in.Tasks, t)
	}
	return in
}

// laterRef reports whether a task after task i has ref, as written.
func laterRef(items []any, i int, ref string) bool {
	for _, item := range items[i+1:] {
		if o, ok := item.(*jsonio.Object); ok {
			if v, ok := o.Get("ref"); ok && v == ref {
				return true
			}
		}
	}
	return false
}

// decodeBatchTask checks one task of a batch, at ptr, and returns it with
// its blocked_by items still unresolved. folder is the batch's.
func decodeBatchTask(p *model.Problems, v any, ptr string, folder model.FolderPath) (BatchTask, []indexed) {
	t := BatchTask{Folder: folder, Tags: []model.Tag{}, BlockedBy: []model.ID{}, Extra: &jsonio.Object{}}
	f, ok := p.Object(v, ptr)
	if !ok {
		return t, nil
	}
	defer f.Done()
	if v, ok := f.Optional("ref"); ok {
		if r, ok := p.Tag(v, f.Ptr("ref")); ok {
			t.Ref = string(r)
		}
	}
	if v, ok := f.Required("title"); ok {
		if s, ok := p.String(v, f.Ptr("title")); ok {
			t.Title, _ = p.TitleInput(s, f.Ptr("title"))
		}
	}
	if v, ok := f.Optional("folder"); ok {
		t.Folder, _ = p.FolderPath(v, f.Ptr("folder"))
	}
	if v, ok := f.Optional("priority"); ok {
		t.Priority, _ = p.Priority(v, f.Ptr("priority"))
	}
	if v, ok := f.Optional("tags"); ok {
		t.Tags, _ = p.Tags(v, f.Ptr("tags"))
	}
	var blockers []indexed
	if v, ok := f.Optional("blocked_by"); ok {
		blockers = batchBlockers(p, v, f.Ptr("blocked_by"))
	}
	if v, ok := f.Optional("extra"); ok {
		if _, ok := p.Object(v, f.Ptr("extra")); ok {
			t.Extra = v.(*jsonio.Object)
		}
	}
	if v, ok := f.Optional("notes"); ok {
		t.Notes, _ = p.String(v, f.Ptr("notes"))
	}
	return t, blockers
}

// batchBlockers checks v, at ptr, as a batch task's blocked_by: a set of
// task IDs and refs. Only the valid, distinct items are returned, in order,
// each with its index.
func batchBlockers(p *model.Problems, v any, ptr string) []indexed {
	a, ok := p.Array(v, ptr)
	if !ok {
		return nil
	}
	items := make([]blocker, len(a))
	valid := make([]bool, len(a))
	for i, item := range a {
		iptr := jsonio.Pointer(ptr, strconv.Itoa(i))
		switch item.(type) {
		case string:
			r, ok := p.Tag(item, iptr)
			items[i], valid[i] = blocker{ref: string(r)}, ok
		case json.Number:
			id, ok := p.ID(item, iptr)
			items[i], valid[i] = blocker{id: id}, ok
		default:
			p.Add(iptr, "expected a task ID or a ref")
		}
	}
	model.Unique(p, items, valid, ptr)
	var out []indexed
	for i, b := range items {
		if valid[i] && !p.Failed(jsonio.Pointer(ptr, strconv.Itoa(i))) {
			out = append(out, indexed{at: i, blocker: b})
		}
	}
	return out
}

// CreateBatchOutput is create-batch's result (create-batch-output).
type CreateBatchOutput struct {
	IDs            []model.ID         `json:"ids"`
	Refs           jsonio.Object      `json:"refs"`
	FoldersCreated []model.FolderPath `json:"folders_created"`
}

// CreateBatchPartial is create-batch's partial result
// (create-batch-partial): what it wrote before failing.
type CreateBatchPartial struct {
	FoldersCreated []model.FolderPath `json:"folders_created"`
	Consumed       []model.ID         `json:"consumed"`
	IDs            []model.ID         `json:"ids"`
	Refs           jsonio.Object      `json:"refs"`
}

// runCreateBatch creates the batch under the write lock. Everything is
// checked first: the path walk of each folder in tree order (a missing
// folder is to be created, not an error), the existing blockers looked up
// and then loaded, then checked against last_id, then the ID ceiling.
// Every task file is encoded before anything is written, so a failure
// midway is always a write's, with its partial. Then it writes in the order its Crash behavior relies on: the
// missing folders in tree order, last_id once, then each task's file and
// .md in input order, so a failure leaves a prefix of the batch.
func runCreateBatch(env Env, in CreateBatchInput, w *errs.Collector) (any, *errs.Error) {
	out := CreateBatchOutput{IDs: []model.ID{}, FoldersCreated: []model.FolderPath{}}
	e := store.Write(env.Env, w, func(tx *store.Tx) *errs.Error {
		missing, e := batchFolders(tx, in.Tasks)
		if e != nil {
			return e
		}
		var existing []model.ID
		for _, t := range in.Tasks {
			for _, id := range t.BlockedBy {
				if !slices.Contains(existing, id) {
					existing = append(existing, id)
				}
			}
		}
		if len(existing) > 0 {
			found, missingIDs, e := lookupIDs(tx, existing)
			if e != nil {
				return e
			}
			if missingIDs != nil {
				return errs.NotFound(nil, missingIDs, nil)
			}
			if e := requireIDs(tx, found); e != nil {
				return e
			}
			if e := aboveLastID(tx, existing); e != nil {
				return e
			}
		}
		last := tx.LastID()
		if last > model.IDMax-int64(len(in.Tasks)) {
			return errs.Conflict(errs.RuleIDExhausted, nil)
		}

		ids := make([]model.ID, len(in.Tasks))
		for i := range ids {
			ids[i] = model.ID(last + 1 + int64(i))
		}
		now := model.TimestampOf(env.Clock())
		files := make([][]byte, len(in.Tasks))
		for i, t := range in.Tasks {
			blockedBy := slices.Clone(t.BlockedBy)
			for _, j := range t.Earlier {
				blockedBy = append(blockedBy, ids[j])
			}
			tf := model.TaskFile{
				Schema: model.TaskSchema, ID: ids[i], Title: t.Title, Priority: t.Priority,
				CreatedAt: now, UpdatedAt: now, BlockedBy: blockedBy, Tags: t.Tags, Extra: t.Extra,
			}
			data, err := tf.Encode()
			if err != nil {
				return errs.Internal("encoding the task file: " + err.Error())
			}
			files[i] = data
		}

		partial := func(e *errs.Error, consumed []model.ID) *errs.Error {
			if len(out.FoldersCreated) == 0 && consumed == nil {
				return e
			}
			if consumed == nil {
				consumed = []model.ID{}
			}
			return e.WithPartial(CreateBatchPartial{FoldersCreated: out.FoldersCreated, Consumed: consumed, IDs: out.IDs, Refs: out.Refs})
		}
		for _, f := range missing {
			created, e := mkdirFolder(tx, f)
			if e != nil {
				return partial(e, nil)
			}
			if created {
				out.FoldersCreated = append(out.FoldersCreated, f)
			}
		}
		if e := tx.SetLastID(int64(ids[len(ids)-1])); e != nil {
			return partial(e, nil)
		}
		for i, t := range in.Tasks {
			loc := store.Location{Folder: t.Folder, ID: ids[i]}
			if e := tx.Create(loc.Rel(), files[i]); e != nil {
				return partial(e, ids)
			}
			out.IDs = append(out.IDs, ids[i])
			if t.Ref != "" {
				out.Refs.Set(t.Ref, ids[i])
			}
			if err := tx.ReplaceRaw(loc.NotesRel(), []byte(t.Notes)); err != nil {
				// As in create: the task exists, so the batch goes on.
				tx.NotesMissing(loc.NotesRel(), ids[i], err)
			}
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}

// batchFolders runs the path walk of every task's folder, in tree order,
// and returns the folders to create, in tree order: each missing folder,
// with every missing one above it. An entry that is not a plain directory
// is the error, the first in tree order.
func batchFolders(tx *store.Tx, tasks []BatchTask) ([]model.FolderPath, *errs.Error) {
	var folders []model.FolderPath
	for _, t := range tasks {
		if !slices.Contains(folders, t.Folder) {
			folders = append(folders, t.Folder)
		}
	}
	slices.SortFunc(folders, store.CompareFolders)
	var missing []model.FolderPath
	for _, f := range folders {
		n, e := tx.WalkFolder(f)
		if e != nil {
			return nil, e
		}
		for i := n + 1; i <= len(f.Segments()); i++ {
			if g := folderPrefix(f, i); !slices.Contains(missing, g) {
				missing = append(missing, g)
			}
		}
	}
	slices.SortFunc(missing, store.CompareFolders)
	// The walk compared each first missing folder with what exists; two
	// missing folders must also differ by more than case.
	for i, f := range missing {
		for _, g := range missing[:i] {
			if store.FoldName(string(f)) == store.FoldName(string(g)) {
				return nil, errs.CaseClash(tx.Path(store.FolderRel(f)), tx.Path(store.FolderRel(g)))
			}
		}
	}
	return missing, nil
}
