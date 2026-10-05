package pick

import (
	"github.com/phansen314/ftask/internal/jsonio"
	"github.com/phansen314/ftask/internal/model"
)

// newAction is n (pick-spec.md, Actions): a new task, titled in the prompt
// new> , which starts with the search query. It is created open, in the
// scope folder with the scope's tags_all, and no priority or blockers.
// After it, the search query is cleared, since it became the title, and
// the cursor goes to the new task if it is in the list.
var newAction = action{key: "n", arity: noTargets, run: openNew, apply: createTask}

func openNew(r *actionRun, _ []shownLine) {
	query, _, e := r.s.Read(queryFile)
	if e != nil {
		r.err = e
		return
	}
	r.openPrompt("n", "new> ", string(query), nil)
}

func createTask(r *actionRun, title string, _ []shownLine) bool {
	var scope Scope
	if r.err = readJSON(r.s, scopeFile, &scope); r.err != nil {
		return false
	}
	in := &jsonio.Object{}
	in.Set("title", title)
	in.Set("folder", string(scope.Folder))
	if len(scope.TagsAll) > 0 {
		tags := make([]any, len(scope.TagsAll))
		for i, t := range scope.TagsAll {
			tags[i] = string(t)
		}
		in.Set("tags", tags)
	}
	out := r.call(0, "create", in, "created", "create")
	task, ok := out.Result.(model.Task)
	if !out.OK || !ok {
		return r.refused() // a refused title stays, to be fixed
	}
	r.outcomes[len(r.outcomes)-1].id = task.ID
	r.clearQuery = true
	r.cursorTo = key(model.TaskView{Task: task})
	return false
}
