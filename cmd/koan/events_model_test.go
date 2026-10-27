package main_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/core"
	"github.com/phansen314/koan/internal/model"
)

// canonical is v as JSON with its object keys sorted, so two values
// compare as JSON, whatever their Go types.
func canonical(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return err.Error()
	}
	var x any
	if err := json.Unmarshal(b, &x); err != nil {
		return err.Error()
	}
	b, _ = json.Marshal(x)
	return string(b)
}

// sameEvent is an event as the model and koan are compared: its kind,
// what it is about, where, and its data, a claim's time written
// $CLAIMED_AT, since the model's clock isn't koan's.
func sameEvent(kind string, id, folder any, path string, data map[string]any) string {
	if _, ok := data["claimed_at"]; ok {
		data["claimed_at"] = "$CLAIMED_AT"
	}
	return canonical(map[string]any{"kind": kind, "id": id, "folder": map[string]any{"id": folder, "path": path}, "data": data})
}

// modelEvents is the model's events, an archived event's paths filled in
// from archive's result, which says where each subject went.
func modelEvents(events []model.Event, archived []any) []string {
	at := map[string]any{}
	for _, a := range archived {
		a := a.(map[string]any)
		at[fmt.Sprintf("$%v.notes", a["id"])] = a["notes_path"]
		at[fmt.Sprintf("$%v.dir", a["id"])] = a["dir"]
	}
	fill := func(v any) any {
		s, ok := v.(string)
		if !ok || !strings.HasPrefix(s, "$") {
			return v
		}
		key, rest, _ := strings.Cut(s, "/")
		if rest == "" {
			return at[key]
		}
		dir, _ := at[key].(string)
		return dir + "/" + rest
	}
	var out []string
	for _, e := range events {
		data := map[string]any{}
		for k, v := range e.Data {
			data[k] = v
		}
		if e.Kind == "archived" {
			data["notes_path"], data["dir"] = fill(data["notes_path"]), fill(data["dir"])
		}
		var id, folder any
		if e.ID != 0 {
			id = e.ID
		}
		if e.Folder != core.RootID {
			folder = e.Folder
		}
		out = append(out, sameEvent(e.Kind, id, folder, e.Path, data))
	}
	return out
}

// koanEvents reads the events recorded after seq through log list, and
// checks they are one change's, made by command, with seq unbroken from
// seq on. It gives them as sameEvent writes them, and the last seq.
func koanEvents(t *testing.T, home string, seq int64, command string) ([]string, int64) {
	t.Helper()
	r := ok(t, home, "log", "list", "--after", fmt.Sprint(seq), "--limit", "null")
	var out []string
	var change int64
	for _, e := range r.Result()["events"].([]any) {
		e := e.(map[string]any)
		s, _ := e["seq"].(json.Number).Int64()
		c, _ := e["change"].(json.Number).Int64()
		if change == 0 {
			change = s
		}
		if s != seq+1 || c != change || e["command"] != command {
			t.Errorf("event %d of change %d by %v after %d, want change %d by %s", s, c, e["command"], seq, change, command)
		}
		seq = s
		f := e["folder"].(map[string]any)
		out = append(out, sameEvent(e["kind"].(string), e["id"], f["id"], f["path"].(string), e["data"].(map[string]any)))
	}
	return out, seq
}

// lastSeqOf is the root's last seq, as info gives it.
func lastSeqOf(t *testing.T, home string) int64 {
	t.Helper()
	n, _ := ok(t, home, "info").Result()["last_seq"].(json.Number).Int64()
	return n
}
