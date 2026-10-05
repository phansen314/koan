package pick

import (
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/model"
)

// The folders in scope, as list has them: the scope folder, then every
// folder under it, or only its immediate subfolders.
func TestFoldersIn(t *testing.T) {
	l := &Load{Folders: []model.FolderPath{"/", "/a", "/a/b", "/a/b/c", "/ab", "/x"}}
	for _, tc := range []struct {
		folder    model.FolderPath
		recursive bool
		want      []model.FolderPath
	}{
		{"/", true, []model.FolderPath{"/", "/a", "/a/b", "/a/b/c", "/ab", "/x"}},
		{"/", false, []model.FolderPath{"/", "/a", "/ab", "/x"}},
		{"/a", true, []model.FolderPath{"/a", "/a/b", "/a/b/c"}},
		{"/a", false, []model.FolderPath{"/a", "/a/b"}},
	} {
		if got := l.foldersIn(Scope{Folder: tc.folder, Recursive: tc.recursive, Folders: true}); !slices.Equal(got, tc.want) {
			t.Errorf("%s %v: %q", tc.folder, tc.recursive, got)
		}
	}
}

// The folder picker whole: folder lines, no actions; Enter emits the chosen
// folders in tree order, and one gone meanwhile in missing.
func TestFolderPicker(t *testing.T) {
	tr := newTestTree(t)
	for _, f := range []string{"/a", "/a/b", "/c"} {
		tr.run("create-folder", map[string]any{"folder": f})
	}
	tr.run("create", map[string]any{"title": "one"})
	out, line := tr.pick(map[string]any{"folders": true, "folder": "/a"}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		if l := helper("lines"); l != "/a\t\t/a\n/a/b\t\t/a/b\n" {
			t.Errorf("lines %q", l)
		}
		helper("command", "")
		if h := helper("text", "header"); h != "[cmd]\n/a\n"+folderHint {
			t.Errorf("header %q", h)
		}
		if h := helper("help"); strings.Contains(h, "complete") || !strings.Contains(h, "first, last line") {
			t.Errorf("help %q", h)
		}
		tr.run("delete-folder", map[string]any{"folder": "/a/b"})
		helper("enter", "", "/a/b", "/a")
	}})
	if !out.OK {
		t.Fatalf("%s", line)
	}
	if got := string(result(t, line)); got != `{"folders":["/a"],"missing":["/a/b"],"actions":[]}` {
		t.Errorf("result %s", got)
	}

	// Quit: an empty selection.
	_, line = tr.pick(map[string]any{"folders": true}, fzfDoes{status: 1, do: func(t *testing.T, helper func(...string) string) {
		helper("quit")
	}})
	if got := string(result(t, line)); got != `{"folders":[],"missing":[],"actions":[]}` {
		t.Errorf("quit: %s", got)
	}
}

// The folder picker binds no action keys.
func TestFolderPickerArgs(t *testing.T) {
	args := strings.Join(picker{exe: "/f", scope: Scope{Folder: "/", Recursive: true, Folders: true}}.args(), " ")
	if strings.Contains(args, "__pick act") {
		t.Errorf("action bound: %s", args)
	}
}
