package core

import "testing"

// The rows of the rules doc's Case table that core decides. The sibling
// rule and the case-only move are the model's; see internal/model.

func TestCaseNamesByteForByte(t *testing.T) {
	// /work exists; /Work is a different name, and reaches it only ignoring
	// case, which is case-mismatch spelling the folder as it is.
	_, err := ResolveString(newFakeTree(), "/Work", WantFolder, nil)
	if !IsKind(err, KindConflict, RuleCaseMismatch) {
		t.Fatalf("got %v, want case-mismatch", err)
	}
	if e := err.(*Error); e.Paths[1] != "/work" {
		t.Errorf("paths = %v, want the folder as spelled second", e.Paths)
	}
}

func TestCaseOneInputTwoSpellings(t *testing.T) {
	refs := func(ss ...string) []Ref {
		var out []Ref
		for _, s := range ss {
			r, err := ParseRef(s)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, r)
		}
		return out
	}
	bad := [][]string{
		{"/Proj/a", "/proj/b"},
		{"/work/Api", "/work/api.done"},
		{"@41/Docs", "41/docs:x"},
	}
	for _, in := range bad {
		if err := CheckCase(refs(in...)); !IsKind(err, KindInvalidInput, "") {
			t.Errorf("CheckCase(%v) = %v, want invalid-input", in, err)
		}
	}
	good := [][]string{
		{"/proj/a", "/proj/b"},
		{"/Proj/a", "/Other/b"},
		{"/a/X", "/b/x"},
		{"@41/Docs", "@42/docs"},
		{"/work", "/work"},
		{"/work:A", "/work:a"}, // material names are never compared
	}
	for _, in := range good {
		if err := CheckCase(refs(in...)); err != nil {
			t.Errorf("CheckCase(%v) = %v", in, err)
		}
	}
}

func TestCaseTagsLowercase(t *testing.T) {
	if ValidTag("Urgent") || !ValidTag("urgent") {
		t.Error("tags are lowercase only")
	}
}

func TestCaseMaterialNeverCompared(t *testing.T) {
	r, err := ResolveString(newFakeTree(), "/work/phase2:DOCS/Plan.MD", WantAny, nil)
	if err != nil || r.Material != "DOCS/Plan.MD" {
		t.Errorf("got %+v, %v", r, err)
	}
}
