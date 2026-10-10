package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A setup ftask left — its config directory, ftask.json, and a temp file —
// is moved to koan's names by the first command, which says so in its
// warnings, and works from then on.
func TestMigrateFromFtask(t *testing.T) {
	tr := newTree(t)
	steps(t, []step{{tr.cmd("create", "carried over"), 0, `"id":1`}})
	// ftask kept its config in its own directory, and no state file; its
	// koan.json was ftask.json, at schema 1, holding last_id.
	ftaskDir := filepath.Join(filepath.Dir(filepath.Join(tr.home, configDir)), "ftask")
	if err := os.MkdirAll(ftaskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(tr.home, configDir, "config.toml"), filepath.Join(ftaskDir, "config.toml")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(tr.home, configDir)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(tr.root(), "koan.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tr.root(), "ftask.json"), []byte(`{"schema": 1, "last_id": 1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tr.root(), ".ftask-tmp-0123"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	// The first command moves both, says so, and then fails like any
	// command on a tree that needs migration.
	r := run(t, tr.cmd("list"))
	envelope(t, r)
	if r.code != 1 || !strings.Contains(r.stdout, `"kind":"migration-pending"`) || strings.Count(r.stdout, `"kind":"migrated"`) != 2 {
		t.Fatalf("first list: exit %d: %s", r.code, r.stdout)
	}
	steps(t, []step{
		{tr.cmd("migrate"), 0, `"state_written":true`},
		{tr.cmd("list"), 0, `carried over`},
		{tr.cmd("create", "next"), 0, `"id":2,`},
	})
	for _, p := range []string{filepath.Join(tr.home, configDir, "config.toml"), filepath.Join(tr.root(), "koan.json")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("after migration: %v", err)
		}
	}
	for _, p := range []string{ftaskDir, filepath.Join(tr.root(), "ftask.json")} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s still there: %v", p, err)
		}
	}
	steps(t, []step{
		{tr.cmd("list"), 0, `"warnings":[]`},
		{tr.cmd("doctor"), 0, `"paths":["` + filepath.Join(tr.root(), ".ftask-tmp-0123") + `"]`},
		{tr.cmd("repair"), 0, `"temp-leftover"`},
	})
	if _, err := os.Lstat(filepath.Join(tr.root(), ".ftask-tmp-0123")); !os.IsNotExist(err) {
		t.Errorf("ftask's temp file not repaired away: %v", err)
	}
}
