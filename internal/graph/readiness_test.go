package graph

import (
	"reflect"
	"testing"

	"github.com/phansen314/koan/internal/model"
)

func TestReadiness(t *testing.T) {
	states := map[model.ID]BlockerState{
		1: BlockerDone, 2: BlockerOpen, 3: BlockerMissing, 4: BlockerDuplicate, 5: BlockerUnusable, 6: BlockerDone,
	}
	done := model.Timestamp("2026-09-21T10:00:00Z")
	for _, tc := range []struct {
		name      string
		blockedBy []model.ID
		completed bool
		want      model.Readiness
		blocking  []model.ID
		asked     []model.ID
	}{
		{"no blockers", nil, false, model.Ready, []model.ID{}, nil},
		{"all done", []model.ID{6, 1}, false, model.Ready, []model.ID{}, []model.ID{1, 6}},
		{"open", []model.ID{1, 2}, false, model.Blocked, []model.ID{2}, []model.ID{1, 2}},
		{"missing", []model.ID{3}, false, model.Blocked, []model.ID{3}, []model.ID{3}},
		{"duplicate", []model.ID{4}, false, model.Blocked, []model.ID{4}, []model.ID{4}},
		{"unusable", []model.ID{5}, false, model.Blocked, []model.ID{5}, []model.ID{5}},
		// Every blocker is evaluated, in ascending order, after the first
		// that blocks.
		{"every blocker asked", []model.ID{6, 5, 4, 3, 2, 1}, false, model.Blocked, []model.ID{2, 3, 4, 5}, []model.ID{1, 2, 3, 4, 5, 6}},
		// A state lookup that misses reads as missing, which blocks.
		{"unset state", []model.ID{7}, false, model.Blocked, []model.ID{7}, []model.ID{7}},
		{"done task", []model.ID{2, 3}, true, model.Done, []model.ID{}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tf := model.TaskFile{BlockedBy: tc.blockedBy}
			tf.Normalize()
			if tc.completed {
				tf.CompletedAt = &done
			}
			var asked []model.ID
			r, blocking := Readiness(&tf, func(id model.ID) BlockerState {
				asked = append(asked, id)
				return states[id]
			})
			if r != tc.want || !reflect.DeepEqual(blocking, tc.blocking) || !reflect.DeepEqual(asked, tc.asked) {
				t.Errorf("got %s %v (asked %v), want %s %v (asked %v)", r, blocking, asked, tc.want, tc.blocking, tc.asked)
			}
		})
	}
}
