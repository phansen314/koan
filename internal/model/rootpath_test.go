package model

import "testing"

func TestCleanPath(t *testing.T) {
	for in, want := range map[string]string{
		"/": "/", "//": "/", "/a/": "/a", "/a//b/./c": "/a/b/c", "/a/../b": "/a/../b", "a/b": "a/b", "": ".", "./": ".",
	} {
		if got := CleanPath(in); got != want {
			t.Errorf("CleanPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHasDotDot(t *testing.T) {
	for in, want := range map[string]bool{
		"/a/../b": true, "/..": true, "..": true, "../a": true, "/a/..": true,
		"/a/b": false, "/a..b": false, "/..a": false, "/a../b": false, "/": false,
	} {
		if got := HasDotDot(in); got != want {
			t.Errorf("HasDotDot(%q) = %v, want %v", in, got, want)
		}
	}
}
