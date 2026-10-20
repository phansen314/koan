// Package ops holds koan's commands, one file each.
package ops

import "github.com/phansen314/koan/internal/cli"

// Commands is every command koan has.
func Commands() []*cli.Command {
	return []*cli.Command{
		Info(),
		Version(),
	}
}
