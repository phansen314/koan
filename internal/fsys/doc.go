// Package fsys is a thin interface over os.Root and flock, with a real
// implementation (OS) and a fault-injecting one (Fault). It is the only
// package that touches the disk.
//
// Errors are returned as the OS gives them — *os.PathError or *os.LinkError
// around a syscall.Errno — because each call site in store decides what an
// errno means there (implementation-spec.md, OS errors). Only the errno is
// meaningful. An error's Path may be relative to the root or joined with it
// (os.Root names an opened file <root>/<name>), and its Op differs between OS
// and Fault; callers pass their own path to errs.
package fsys
