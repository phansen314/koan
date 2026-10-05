package model

import "strings"

// CleanPath removes a path's empty and "." segments and any trailing "/",
// lexically: ".." segments are kept, and symlinks are not resolved (see
// design-spec.md, Root path).
func CleanPath(path string) string {
	abs := strings.HasPrefix(path, "/")
	var segs []string
	for _, s := range strings.Split(path, "/") {
		if s != "" && s != "." {
			segs = append(segs, s)
		}
	}
	joined := strings.Join(segs, "/")
	switch {
	case abs:
		return "/" + joined
	case joined == "":
		return "."
	}
	return joined
}

// HasDotDot reports whether path has a ".." segment, which no root path may
// have: removing it lexically can change which directory is meant when an
// earlier segment is a symlink.
func HasDotDot(path string) bool {
	return strings.Contains("/"+path+"/", "/../")
}
