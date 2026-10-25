package model

// The tests name changes by the kind they act on; these give each
// through the change that now takes both kinds.

func (t *Tree) UpdateTask(task string, u Update) (bool, error) {
	ids, err := t.Update([]string{task}, u)
	return len(ids) > 0, err
}

func (t *Tree) UpdateFolder(folder string, u Update) (bool, error) {
	ids, err := t.Update([]string{folder}, u)
	return len(ids) > 0, err
}

func (t *Tree) MoveTask(into string, tasks ...string) (MoveResult, error) {
	return t.Move(MvInto, into, tasks...)
}

func (t *Tree) MoveFolder(mv Mv, dest string, folders ...string) (MoveResult, error) {
	return t.Move(mv, dest, folders...)
}

func (t *Tree) DeleteTask(tasks ...string) (Removed, error) {
	return t.Delete(tasks, nil, nil)
}

func (t *Tree) DeleteFolder(folders []string, contents, material bool) (Removed, error) {
	return t.Delete(folders, &contents, &material)
}
