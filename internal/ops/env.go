package ops

import (
	"time"

	"github.com/phansen314/koan/internal/fsys"
	"github.com/phansen314/koan/internal/store"
)

// Env is an invocation's environment, passed in, never global
// (implementation-spec.md, Environment): the filesystem and the home and
// config directories store needs, and the clock.
type Env struct {
	store.Env
	Clock Clock
}

// NewEnv is the process's environment: the real filesystem, the home and
// config directories located from getenv (design-spec.md, Config file), the
// default lock wait, ftask's config directory to migrate from, and the real
// clock. goos is runtime.GOOS.
func NewEnv(getenv func(string) string, goos string) Env {
	home, configDir := store.Locate(getenv, goos)
	legacy := store.LocateLegacy(getenv, goos)
	return Env{Env: store.Env{FS: fsys.OS{}, Home: home, ConfigDir: configDir, LockWait: store.DefaultLockWait, LegacyConfigDir: legacy}, Clock: RealClock}
}

// Clock returns the current time. Operations stamp files with it, so tests
// use a fixed one and compare written files byte for byte.
type Clock func() time.Time

// RealClock is the system time in UTC, truncated to whole seconds
// (design-spec.md, Timestamps).
func RealClock() time.Time {
	return time.Now().UTC().Truncate(time.Second)
}
