package core

import (
	"reflect"
	"testing"
)

// Every row of the rules doc's References table.
func TestParseRefReferencesTable(t *testing.T) {
	tests := []struct {
		in   string
		want Ref
	}{
		{"42", Ref{Kind: TargetID, ID: 42}},
		{"@42", Ref{Kind: TargetID, ID: 42}},
		{"@9.start", Ref{Kind: TargetID, ID: 9, End: EndStart}},
		{"@9.done", Ref{Kind: TargetID, ID: 9, End: EndDone}},
		{"/", Ref{Kind: TargetPath}},
		{"/work/api", Ref{Kind: TargetPath, Names: []string{"work", "api"}}},
		{"/work/api.start", Ref{Kind: TargetPath, Names: []string{"work", "api"}, End: EndStart}},
		{"/work/api.done", Ref{Kind: TargetPath, Names: []string{"work", "api"}, End: EndDone}},
		{"41/phase2", Ref{Kind: TargetAnchored, ID: 41, Names: []string{"phase2"}}},
		{"@41/phase2", Ref{Kind: TargetAnchored, ID: 41, Names: []string{"phase2"}}},
		{"41:docs/plan.md", Ref{Kind: TargetID, ID: 41, Material: "docs/plan.md", HasMaterial: true}},
		{"@41:docs/plan.md", Ref{Kind: TargetID, ID: 41, Material: "docs/plan.md", HasMaterial: true}},
		{"/work/api:docs/plan.md", Ref{Kind: TargetPath, Names: []string{"work", "api"}, Material: "docs/plan.md", HasMaterial: true}},
		{"@41/phase2:docs/plan.md", Ref{Kind: TargetAnchored, ID: 41, Names: []string{"phase2"}, Material: "docs/plan.md", HasMaterial: true}},
		{"$a", Ref{Kind: TargetLabel, Label: "$a"}},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseRef(tt.in)
			if err != nil {
				t.Fatalf("ParseRef(%q): %v", tt.in, err)
			}
			tt.want.Raw = tt.in
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseRef(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

// Resolution steps 1 and 2: how a reference is split.
func TestParseRefSplitting(t *testing.T) {
	tests := []struct {
		in   string
		want Ref
	}{
		// Step 1: the first ":" splits; the material may hold more.
		{"41:a:b", Ref{Kind: TargetID, ID: 41, Material: "a:b", HasMaterial: true}},
		{"/:top.txt", Ref{Kind: TargetPath, Material: "top.txt", HasMaterial: true}},
		// Step 2: the end is split from the target, never the material.
		{"41:notes.done", Ref{Kind: TargetID, ID: 41, Material: "notes.done", HasMaterial: true}},
		{"41:x.start", Ref{Kind: TargetID, ID: 41, Material: "x.start", HasMaterial: true}},
		// Below the first part, @ means nothing.
		{"@41:node_modules/@types/x", Ref{Kind: TargetID, ID: 41, Material: "node_modules/@types/x", HasMaterial: true}},
		// Hidden material is material.
		{"41:.git/config", Ref{Kind: TargetID, ID: 41, Material: ".git/config", HasMaterial: true}},
		{"41:..x", Ref{Kind: TargetID, ID: 41, Material: "..x", HasMaterial: true}},
		{"@41/a/b.start", Ref{Kind: TargetAnchored, ID: 41, Names: []string{"a", "b"}, End: EndStart}},
		{"999999999999999", Ref{Kind: TargetID, ID: 999999999999999}},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseRef(tt.in)
			if err != nil {
				t.Fatalf("ParseRef(%q): %v", tt.in, err)
			}
			tt.want.Raw = tt.in
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseRef(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseRefInvalid(t *testing.T) {
	for _, in := range []string{
		"",
		"41.start:x",       // an end before the ":"
		"$a.start",         // a label takes no end
		"$a:x",             // nor material
		"$",                // a label is "$" and a name
		"$-a",              //
		"0",                // IDs start 1–9
		"042",              //
		"1000000000000000", // sixteen digits
		"@",                //
		"@@4",              //
		"-4",               //
		"41.begin",         // the only ends are start and done
		"41.start.done",
		"/work/",       // an empty name
		"//work",       //
		"/work/a.b/c",  // a name never holds a "."
		"/wo rk",       //
		"/-work",       //
		"41/",          //
		"41:",          // empty material
		"41:a//b",      // an empty part
		"41:./a",       // a "." part
		"41:a/..",      // a ".." part
		"41:@notes.md", // the first part not starting with "@"
		"41:a\x00b",    //
		"@/work",       //
		"work",         // a path starts with "/"
	} {
		if _, err := ParseRef(in); err == nil {
			t.Errorf("ParseRef(%q) succeeded, want invalid-input", in)
		} else if !IsKind(err, KindInvalidInput, "") {
			t.Errorf("ParseRef(%q) = %v, want invalid-input", in, err)
		}
	}
}

func TestParseRefRootEnd(t *testing.T) {
	ref, err := ParseRef("/.start")
	if err != nil {
		t.Fatal(err)
	}
	if !ref.IsRoot() || ref.End != EndStart {
		t.Fatalf("ParseRef(/.start) = %+v", ref)
	}
	if _, err := Resolve(newFakeTree(), ref, WantEdge, nil); !IsKind(err, KindInvalidInput, "") {
		t.Errorf("the root's gate: %v, want invalid-input", err)
	}
}

func TestKeptText(t *testing.T) {
	tests := []struct{ got, want string }{
		{Kept(9, "/work/phase2", EndNone), "@9 (/work/phase2)"},
		{KeptMaterial(9, "/work/phase2", "docs/plan.md"), "@9:docs/plan.md (/work/phase2:docs/plan.md)"},
		{Kept(57, "/work/api", EndDone), "@57 (/work/api.done)"},
		{Kept(56, "/work/api", EndStart), "@56 (/work/api.start)"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("got %q, want %q", tt.got, tt.want)
		}
	}
}
