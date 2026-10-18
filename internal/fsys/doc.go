// Package fsys is a thin layer over os.Root and flock, with a hook before
// every call for injecting faults (fault.go). It is the only package that
// touches the disk.
//
// Errors are returned as the OS gives them — *os.PathError or *os.LinkError
// around a syscall.Errno — because each call site in store decides what an
// errno means there (implementation-spec.md, OS errors). Only the errno is
// meaningful. An error's Path may be relative to the root or joined with it
// (os.Root names an opened file <root>/<name>), and its Op differs between a
// real error and an injected one; callers pass their own path to errs.
package fsys
