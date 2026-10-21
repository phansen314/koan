// Command koan is task management for one person on one machine.
package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/phansen314/koan/internal/cli"
	"github.com/phansen314/koan/internal/ops"
)

func main() {
	// A closed standard output is then a failed write, exit 3, not a kill.
	signal.Ignore(syscall.SIGPIPE)
	os.Exit(cli.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, ops.Commands()))
}
