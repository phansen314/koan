package core

import (
	"strings"
	"testing"
)

func TestValidName(t *testing.T) {
	for _, s := range []string{"a", "Z", "9", "work", "HOME-12345", "a-b", strings.Repeat("a", 64)} {
		if !ValidName(s) {
			t.Errorf("ValidName(%q) = false", s)
		}
	}
	for _, s := range []string{"", "-a", "a-", "a.b", "a:b", "a/b", "a b", "a_b", "é", strings.Repeat("a", 65)} {
		if ValidName(s) {
			t.Errorf("ValidName(%q) = true", s)
		}
	}
}

func TestValidTag(t *testing.T) {
	for _, s := range []string{"urgent", "a-1", "9"} {
		if !ValidTag(s) {
			t.Errorf("ValidTag(%q) = false", s)
		}
	}
	for _, s := range []string{"", "Urgent", "-a", "a-", "a.b", strings.Repeat("a", 65)} {
		if ValidTag(s) {
			t.Errorf("ValidTag(%q) = true", s)
		}
	}
}

func TestValidLabel(t *testing.T) {
	for _, s := range []string{"$a", "$design", "$Build-2"} {
		if !ValidLabel(s) {
			t.Errorf("ValidLabel(%q) = false", s)
		}
	}
	for _, s := range []string{"", "$", "a", "$-a", "$a.b", "$$a"} {
		if ValidLabel(s) {
			t.Errorf("ValidLabel(%q) = true", s)
		}
	}
}

func TestParseID(t *testing.T) {
	for s, want := range map[string]int64{"1": 1, "42": 42, "999999999999999": MaxID} {
		if got, ok := ParseID(s); !ok || got != want {
			t.Errorf("ParseID(%q) = %d, %v", s, got, ok)
		}
	}
	for _, s := range []string{"", "0", "01", "-1", "@1", "1000000000000000", "1.0"} {
		if _, ok := ParseID(s); ok {
			t.Errorf("ParseID(%q) succeeded", s)
		}
	}
	if ValidID(0) || ValidID(MaxID+1) || !ValidID(1) || !ValidID(MaxID) {
		t.Error("ValidID range")
	}
}

func TestCheckTitle(t *testing.T) {
	ok := map[string]string{
		"Book flights":           "Book flights",
		"  padded\t":             "padded",
		"日本":                     "日本",
		strings.Repeat("é", 200): strings.Repeat("é", 200),
	}
	for in, want := range ok {
		got, err := CheckTitle(in)
		if err != nil || got != want {
			t.Errorf("CheckTitle(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{
		"", "   ", strings.Repeat("a", 201), "a\nb", "a\rb", "a\tb", "a\x00b", "a\x7fb", "a\u0085b",
		"a b", "a b", "\xff",
	} {
		if _, err := CheckTitle(in); !IsKind(err, KindInvalidInput, "") {
			t.Errorf("CheckTitle(%q) = %v, want invalid-input", in, err)
		}
	}
}

func TestParseInt(t *testing.T) {
	for s, want := range map[string]int64{"0": 0, "-5": -5, "9007199254740991": MaxInt, "-9007199254740991": -MaxInt} {
		if got, err := ParseInt(s); err != nil || got != want {
			t.Errorf("ParseInt(%q) = %d, %v", s, got, err)
		}
	}
	for _, s := range []string{"", "1.0", "1e3", "01", "+1", "9007199254740992", "-9007199254740992", "99999999999999999999"} {
		if _, err := ParseInt(s); !IsKind(err, KindInvalidInput, "") {
			t.Errorf("ParseInt(%q) = %v, want invalid-input", s, err)
		}
	}
	if CheckInt(MaxInt) != nil || CheckInt(MaxInt+1) == nil || CheckInt(-MaxInt-1) == nil {
		t.Error("CheckInt range")
	}
}

func TestParseTimestamp(t *testing.T) {
	for _, s := range []string{"2026-10-20T12:00:00Z", "2024-02-29T23:59:59Z", "0001-01-01T00:00:00Z"} {
		tm, err := ParseTimestamp(s)
		if err != nil || FormatTimestamp(tm) != s {
			t.Errorf("ParseTimestamp(%q) = %v, %v", s, tm, err)
		}
	}
	for _, s := range []string{
		"", "2026-10-20", "2026-10-20T12:00:00", "2026-10-20T12:00:00+00:00", "2026-10-20T12:00:00.5Z",
		"2026-10-20 12:00:00Z", "2025-02-29T00:00:00Z", "2026-13-01T00:00:00Z", "2026-10-20T24:00:00Z",
		"2026-10-20T12:60:00Z", "2026-12-31T23:59:60Z", "2026-10-20t12:00:00z",
	} {
		if _, err := ParseTimestamp(s); !IsKind(err, KindInvalidInput, "") {
			t.Errorf("ParseTimestamp(%q) = %v, want invalid-input", s, err)
		}
	}
}

func TestCheckMaterial(t *testing.T) {
	for _, s := range []string{"docs/plan.md", ".git", "a/@types/x", "..x", "a.b/c", "a:b"} {
		if err := CheckMaterial(s); err != nil {
			t.Errorf("CheckMaterial(%q): %v", s, err)
		}
	}
	for _, s := range []string{"", "/a", "a/", "a//b", ".", "..", "a/./b", "a/../b", "@x", "@notes.md", "a\x00"} {
		if err := CheckMaterial(s); err == nil {
			t.Errorf("CheckMaterial(%q) succeeded", s)
		}
	}
}
