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
		Update(),
		TaskComplete(),
		TaskReopen(),
		TaskBlock(),
		TaskUnblock(),
		Join(),
		Unjoin(),
		Mv(),
		Delete(),
		Archive(),
		TaskClaim(),
		TaskClaimNext(),
		TaskUnclaim(),
		FolderCreate(),
		FolderList(),
		Batch(),
		Where(),
		Context(),
		Export(),
		LogList(),
		Bind(),
		Unbind(),
		Init(),
		Info(),
		Version(),
		Doctor(),
		Repair(),
		Backup(),
		Pick(),
	})
}
