// Package ops holds koan's commands, one file each.
package ops

import "github.com/phansen314/koan/internal/cli"

// Commands is every command koan has.
func Commands() []*cli.Command {
	return describe([]*cli.Command{
		TaskCreate(),
		Show(),
		TaskList(),
		TaskFrontier(),
		TaskWhy(),
		TaskUpdate(),
		TaskComplete(),
		TaskReopen(),
		TaskBlock(),
		TaskUnblock(),
		Join(),
		Unjoin(),
		TaskMv(),
		TaskDelete(),
		Archive(),
		TaskClaim(),
		TaskClaimNext(),
		TaskUnclaim(),
		FolderCreate(),
		FolderList(),
		FolderUpdate(),
		FolderMv(),
		FolderDelete(),
		Batch(),
		Where(),
		Context(),
		Export(),
		MarkerBind(),
		MarkerUnbind(),
		Init(),
		Info(),
		Version(),
		Doctor(),
		Repair(),
		Backup(),
		Pick(),
	})
}
