package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// koan migrate against the built binary: a schema 1 tree refuses every
// operation that needs a usable root until migrate has run, and works after.
func TestMigrateCommand(t *testing.T) {
	tr := newTree(t)
	steps(t, []step{{tr.cmd("create", "carried over"), 0, `"id":1`}})
	meta := filepath.Join(tr.root(), "koan.json")
	if err := os.WriteFile(meta, []byte(`{"schema": 1, "last_id": 1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	steps(t, []step{
		{tr.cmd("list"), 1, `"kind":"migration-pending","message":"the tree needs migration: it records step 0, this binary's latest is 1; run koan migrate","details":{"recorded":0,"latest":1}`},
		{tr.cmd("info"), 0, `"metadata":"old-format","schema":1,"migration":0`},
		{tr.cmd("version"), 0, `"migration":1`},
		{tr.cmd("migrate", "--dry-run"), 0, `"dry_run":true,"from":0,"to":1,"applied":[{"step":1,"name":"tree-marker"}],"changed":true,"metadata_converted":true,"state_written":false,"tasks_converted":0,"unconverted":[],"unconverted_count":0`},
		{tr.cmd("info"), 0, `"migration_pending":true`},
		{tr.cmd("migrate"), 0, `"dry_run":false,"from":0,"to":1`},
		{tr.cmd("list"), 0, `carried over`},
		{tr.cmd("migrate"), 0, `"applied":[],"changed":false`},
		{tr.cmd("info"), 0, `"usable":true,"compatible":true,"migration_pending":false`},
	})
	b, err := os.ReadFile(meta)
	if err != nil || string(b) != "{\n  \"schema\": 2,\n  \"migration\": 1\n}\n" {
		t.Errorf("koan.json %q %v", b, err)
	}
	// -i, and a bad field.
	in := tr.cmd("migrate", "-i", "-")
	stdin(in, `{"dry_run": true}`)
	steps(t, []step{{in, 0, `"dry_run":true`}})
	bad := tr.cmd("migrate", "-i", "-")
	stdin(bad, `{"dry_run": 1}`)
	steps(t, []step{{bad, 1, `"kind":"invalid-input"`}})
}

// A tree copied to a new machine (a new HOME): init attaches it, with the
// highest ID in the copy as the counter, and the next create goes above it.
// The original machine's counter is not in the tree, so nothing carries over.
func TestCloneAttach(t *testing.T) {
	src := newTree(t)
	steps(t, []step{
		{src.cmd("create", "a"), 0, `"id":1,`},
		{src.cmd("create", "b"), 0, `"id":2,`},
		{src.cmd("create", "c"), 0, `"id":3,`},
		{src.cmd("delete", "3"), 0, `"id":3,"folder"`},
	})
	dst := koan(t, "init", "~/copy")
	home := envHome(dst)
	copyRoot := filepath.Join(home, "copy")
	if err := os.MkdirAll(copyRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(src.root())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(src.root(), e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(copyRoot, e.Name()), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	steps(t, []step{
		{dst, 0, `"action":"attached","last_id":2`},
		{koanIn(dst, "create", "d"), 0, `"id":3,`},
	})
}

// koanIn is the binary with args, in the environment of cmd.
func koanIn(cmd *exec.Cmd, args ...string) *exec.Cmd {
	out := exec.Command(binary, args...)
	out.Env = append([]string(nil), cmd.Env...)
	return out
}
