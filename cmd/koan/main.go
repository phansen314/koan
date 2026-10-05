// Command koan is the koan CLI. main sets up the process and exits; all
// behavior is in internal/cli (implementation-spec.md, Exit and signals).
package main

import (
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/phansen314/koan/internal/cli"
)

// envHook runs once the process is set up, and may change its environment;
// test builds set it (hook_e2e.go).
var envHook = func(*cli.Env) {}

func main() {
	os.Exit(run())
}

// run is everything but the exit, so deferred cleanup always runs first.
func run() int {
	// A panic or fatal error ends by SIGABRT (exit 134, outcome unknown),
	// not Go's default exit 2, which would read as a usage error.
	debug.SetTraceback("crash")
	// Writing to a closed pipe then returns EPIPE, reported as exit 3,
	// instead of killing the process.
	signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE)
	env := cli.ProcessEnv()
	envHook(&env)
	return cli.Run(os.Args[1:], env)
}
