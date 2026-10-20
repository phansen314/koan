package store

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"testing"

	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/model"
)

func clock() func() string {
	n := 0
	return func() string {
		n++
		return fmt.Sprintf("2026-10-20T10:%02d:%02dZ", n/60, n%60)
	}
}

// mirror writes the model's rows into the store, so both hold one tree.
func mirror(t *testing.T, s *Store, m *model.Tree) {
	t.Helper()
	write(t, s, func(tx *Tx) error {
		for id := int64(1); id <= m.Issued; id++ {
			kind, state, archive := "task", "deleted", any(nil)
			if _, ok := m.Folders[id]; ok {
				kind = "folder"
			}
			if _, ok := m.Tasks[id]; ok || kind == "folder" {
				state = "live"
			}
			if _, ok := m.Index[id]; ok {
				state, archive = "archived", fmt.Sprintf("/archive/%d", id)
			}
			if _, err := tx.Exec("INSERT INTO issued (id, kind, state, archive) VALUES (?, ?, ?, ?)", id, kind, state, archive); err != nil {
				return err
			}
		}
		folders := slices.Collect(maps.Values(m.Folders))
		depth := func(f *model.Folder) int { return len(m.Path(f.ID)) }
		slices.SortFunc(folders, func(a, b *model.Folder) int { return depth(a) - depth(b) })
		for _, f := range folders {
			if _, err := tx.Exec("INSERT INTO folder (id, parent, name, extra) VALUES (?, ?, ?, '{}')", f.ID, parentArg(f.Parent), f.Name); err != nil {
				return err
			}
		}
		for _, x := range m.Tasks {
			var gate any
			if x.Gate != core.EndNone {
				gate = string(x.Gate)
			}
			extra, _ := json.Marshal(x.Extra)
			if _, err := tx.Exec(`INSERT INTO task (id, folder, gate, title, priority, created_at, completed_at, updated_at, extra)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, x.ID, parentArg(x.Folder), gate, x.Title, x.Priority,
				x.CreatedAt, x.CompletedAt, x.UpdatedAt, string(extra)); err != nil {
				return err
			}
		}
		for _, x := range m.Tasks {
			for b := range x.BlockedBy {
				if _, err := tx.Exec("INSERT INTO edge (task, blocker) VALUES (?, ?)", x.ID, b); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func noErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// exampleTree is a small tree with nested folders, gates, edges across
// folders and a complete task.
func exampleTree(t *testing.T) *model.Tree {
	m := model.New(1, clock())
	_, err := m.CreateFolder("/work/api", nil, nil)
	noErr(t, err)
	_, err = m.CreateFolder("/home", nil, nil)
	noErr(t, err)
	for _, in := range []model.TaskInput{
		{Title: "design", Folder: "/work/api", Blocks: []string{"/work/api.start"}},
		{Title: "build", Folder: "/work/api", BlockedBy: []string{"/work/api.start"}, Blocks: []string{"/work/api"}},
		{Title: "test", Folder: "/work/api", BlockedBy: []string{"/work/api.start"}, Blocks: []string{"/work/api"}},
		{Title: "ship", Folder: "/work", BlockedBy: []string{"/work/api"}},
		{Title: "shop", Folder: "/home"},
		{Title: "loose"},
	} {
		_, err := m.CreateTask(in)
		noErr(t, err)
	}
	_, err = m.Complete("/home.done")
	if err == nil {
		t.Fatal("completing a gate that doesn't exist worked")
	}
	return m
}

// Resolution and readiness give the same answers against SQLite as
// against the model.
func TestAgreesWithModel(t *testing.T) {
	m := exampleTree(t)
	_, s := newRoot(t, 1)
	mirror(t, s, m)

	shop := findTask(m, "shop")
	_, err := m.Complete(fmt.Sprint(shop))
	noErr(t, err)
	write(t, s, func(tx *Tx) error {
		_, err := tx.Exec("UPDATE task SET completed_at = ? WHERE id = ?", *m.Tasks[shop].CompletedAt, shop)
		return err
	})

	refs := []string{"/", "/work", "/work/api", "/Work", "/work/API", "/work/nope", "/home", "/work/api.start", "/work/api.done",
		"/home.done", "@1", "@2", "@99", "@2/api", "@2/API", "@1.start", "/work:notes.md", "@0"}
	for id := range m.Tasks {
		refs = append(refs, fmt.Sprint(id), fmt.Sprintf("@%d.done", id))
	}
	wants := []core.Want{core.WantFolder, core.WantTask, core.WantEdge, core.WantAny}
	s.Read(func(tx *Tx) error {
		for _, ref := range refs {
			for _, want := range wants {
				gm, em := core.ResolveString(m, ref, want, nil)
				gs, es := core.ResolveString(tx, ref, want, nil)
				if !reflect.DeepEqual(gm, gs) || !reflect.DeepEqual(em, es) {
					t.Errorf("%s as %d: model %+v %v, store %+v %v", ref, want, gm, em, gs, es)
				}
			}
		}
		for id := range m.Tasks {
			if core.Ready(m, id) != core.Ready(tx, id) || core.Blocked(m, id) != core.Blocked(tx, id) {
				t.Errorf("@%d: model ready %v, store ready %v", id, core.Ready(m, id), core.Ready(tx, id))
			}
			if !slices.Equal(m.BlockedBy(id), tx.BlockedBy(id)) {
				t.Errorf("@%d blocked_by: model %v, store %v", id, m.BlockedBy(id), tx.BlockedBy(id))
			}
		}
		for id := range m.Folders {
			if m.Path(id) != tx.Path(id) {
				t.Errorf("@%d path: model %s, store %s", id, m.Path(id), tx.Path(id))
			}
		}
		return tx.err
	})

	// Deleting each task in turn releases what the model releases.
	for _, title := range []string{"design", "build"} {
		id := findTask(m, title)
		want, err := m.DeleteTask(fmt.Sprint(id))
		noErr(t, err)
		var got []int64
		write(t, s, func(tx *Tx) error {
			var err error
			got, err = tx.RemoveTasks([]int64{id}, nil)
			return err
		})
		if title == "design" && len(got) == 0 {
			t.Errorf("deleting design released nothing; the start gate waited only on it")
		}
		if !slices.Equal(got, want.Released) {
			t.Errorf("deleting %s: released %v, model %v", title, got, want.Released)
		}
	}
}

func findTask(m *model.Tree, title string) int64 {
	for id, x := range m.Tasks {
		if x.Title == title {
			return id
		}
	}
	return 0
}
