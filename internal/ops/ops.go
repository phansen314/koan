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
		TaskJoin(),
		TaskUnjoin(),
		TaskMv(),
		TaskDelete(),
		TaskArchive(),
		TaskClaim(),
		TaskClaimNext(),
		TaskRelease(),
		FolderCreate(),
		FolderList(),
		FolderUpdate(),
		FolderMv(),
		FolderJoin(),
		FolderUnjoin(),
		FolderDelete(),
		FolderArchive(),
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
