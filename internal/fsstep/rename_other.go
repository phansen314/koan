//go:build !linux && !darwin

package fsstep

func renameNoReplace(from, to string) error { return errNoReplace }
