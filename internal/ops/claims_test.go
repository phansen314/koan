package ops

import (
	"os"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/schematest"
)

func TestClaims(t *testing.T) {
	w := ready(t)
	w.ok("create-batch", `{"tasks":[{"ref":"a","title":"a","priority":1},{"title":"b","priority":2},{"title":"c","blocked_by":["a"]}]}`)
	first := w.ok("claim-next", `{"extra":{"worker":"w1"}}`)
	if tk := task(first); tk["id"] != float64(2) || tk["claim"] == nil {
		t.Errorf("claim-next: %v", first)
	}
	wantIDs(t, "frontier hides the claimed task", w.ok("frontier", `{}`)["tasks"], 1)
	wantIDs(t, "list --claimed", w.ok("list", `{"claimed":true}`)["tasks"], 2)
	e := w.fail("claim", `{"id":2}`, errs.Conflict, errs.RuleClaimed)
	if !strings.Contains(e.Message, "w1") {
		t.Errorf("the refusal does not say who holds the claim: %s", e.Message)
	}
	w.ok("claim", `{"id":3}`) // a blocked task can be reserved
	w.ok("claim-next", `{}`)
	if got := w.ok("claim-next", `{}`); got["task"] != nil {
		t.Errorf("claim-next with nothing left: %v", got)
	}
	if got := w.ok("release", `{"id":2}`); got["changed"] != true {
		t.Errorf("release: %v", got)
	}
	if got := w.ok("release", `{"id":2}`); got["changed"] != false {
		t.Errorf("release twice: %v", got)
	}
	w.ok("release", `{"id":999}`)
	w.ok("done", `{"id":1}`)
	if tk := task(w.ok("show", `{"id":1}`)); tk["claim"] != nil {
		t.Errorf("a done task still shows a claim: %v", tk)
	}
	w.fail("claim", `{"id":1}`, errs.Conflict, errs.RuleTaskDone)
	if n := w.ok("info", `{}`)["claims"]; n != float64(1) {
		t.Errorf("claims left: %v, want only task 3's", n)
	}
	schematest.Must(t, "state-file", readJSON(t, w.env.StatePath()))
}

func TestClaimsEndWithTheirTasks(t *testing.T) {
	w := ready(t)
	w.ok("create-batch", `{"tasks":[{"title":"a"},{"title":"b"}]}`)
	w.ok("claim", `{"id":1}`)
	w.ok("claim", `{"id":2}`)
	w.ok("delete", `{"id":1}`)
	if n := w.ok("info", `{}`)["claims"]; n != float64(1) {
		t.Errorf("delete left the claim: %v claims", n)
	}
	// A claim left on a done task, as a crash between done's two writes
	// leaves it, must not come back when the task is reopened.
	done := "2026-10-11T12:00:00Z"
	os.WriteFile(w.root("2.json"), []byte(strings.Replace(w.file("2.json"), `"completed_at": null`, `"completed_at": "`+done+`"`, 1)), 0o644)
	if tk := task(w.ok("reopen", `{"id":2}`)); tk["claim"] != nil {
		t.Errorf("a reopened task is claimed: %v", tk["claim"])
	}
	wantIDs(t, "frontier", w.ok("frontier", `{}`)["tasks"], 2)
}

func TestClaimedFilter(t *testing.T) {
	w := ready(t)
	w.ok("create-batch", `{"tasks":[{"title":"a"},{"title":"b"}]}`)
	w.ok("claim", `{"id":1}`)
	wantIDs(t, "claimed", w.ok("list", `{"claimed":true}`)["tasks"], 1)
	wantIDs(t, "unclaimed", w.ok("list", `{"claimed":false}`)["tasks"], 2)
	wantIDs(t, "both", w.ok("list", `{}`)["tasks"], 1, 2)
}
