package markers

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// realTemp is a temp directory by its real path: macOS's sit behind a
// symlink.
func realTemp(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestReal(t *testing.T) {
	d := realTemp(t)
	os.MkdirAll(filepath.Join(d, "a", "b"), 0o755)
	os.Symlink(filepath.Join(d, "a"), filepath.Join(d, "abs"))
	os.Symlink("a/b", filepath.Join(d, "rel"))
	os.Symlink("../b", filepath.Join(d, "a", "b", "up"))
	os.Symlink("hop", filepath.Join(d, "hop2"))
	os.Symlink("hop2", filepath.Join(d, "hop"))
	os.WriteFile(filepath.Join(d, "a", "f"), nil, 0o644)
	for _, c := range []struct {
		in, want string
		exists   bool
	}{
		{d, d, true},
		{d + "/a/b", d + "/a/b", true},
		{d + "/abs/b", d + "/a/b", true},
		{d + "/rel", d + "/a/b", true},
		{d + "/rel/up/up", d + "/a/b", true},
		{d + "/abs/missing/x", d + "/a/missing/x", false},
		{d + "/a/f/x", d + "/a/f/x", false},
		// A name is found byte for byte, never by the filesystem's case
		// rules, so this holds on macOS too.
		{d + "/A/b", d + "/A/b", false},
	} {
		got, exists, err := Real(c.in)
		if err != nil || got != c.want || exists != c.exists {
			t.Errorf("Real(%s) = %s, %v, %v; want %s, %v", c.in, got, exists, err, c.want, c.exists)
		}
	}
	if _, _, err := Real(d + "/hop/x"); err == nil {
		t.Errorf("a symlink loop resolves")
	}
}

func TestUnder(t *testing.T) {
	for _, c := range []struct {
		p, dir string
		want   bool
	}{
		{"/h/koan-root", "/h/koan-root", true},
		{"/h/koan-root/tree", "/h/koan-root", true},
		{"/h/koan-root-2", "/h/koan-root", false},
		{"/h", "/h/koan-root", false},
		{"/x", "/", true},
	} {
		if got := Under(c.p, c.dir); got != c.want {
			t.Errorf("Under(%s, %s) = %v", c.p, c.dir, got)
		}
	}
}

func TestRead(t *testing.T) {
	d := realTemp(t)
	os.Mkdir(filepath.Join(d, DirName), 0o755)
	file := File(d)
	q := fmt.Sprintf("%q", d)
	for _, c := range []struct{ content, why string }{
		{`{"schema":1,"id":2,"path":"/work/api","dir":` + q + `}`, ""},
		{`{"schema":1,"id":2,"path":"/work/api","dir":` + q + `,"new":{"x":1}}`, ""},
		{`{"schema":1,"id":2,"path":"/work/api","dir":"/elsewhere"}`, "copied or moved"},
		{`{"id":2,"path":"/work/api","dir":` + q + `}`, "lacks schema"},
		{`{"schema":2}`, "schema 2"},
		{`{"schema":"1","id":2,"path":"/work/api","dir":` + q + `}`, `schema "1"`},
		{`{"schema":1,"path":"/work/api","dir":` + q + `}`, "lacks id"},
		{`{"schema":1,"id":0,"path":"/work/api","dir":` + q + `}`, "isn't an ID"},
		{`{"schema":1,"id":"2","path":"/work/api","dir":` + q + `}`, "isn't an ID"},
		{`{"schema":1,"id":2.5,"path":"/work/api","dir":` + q + `}`, "isn't an ID"},
		{`{"schema":1,"id":2,"dir":` + q + `}`, "lacks path"},
		{`{"schema":1,"id":2,"path":"/","dir":` + q + `}`, "isn't a folder's path"},
		{`{"schema":1,"id":2,"path":"/a_b","dir":` + q + `}`, "isn't a folder's path"},
		{`{"schema":1,"id":2,"path":"/work/api"}`, "lacks dir"},
		{`{"schema":1,"id":2,"path":"/work/api","dir":"rel"}`, "isn't a real path"},
		{`{"schema":1,"id":2,"path":"/work/api","dir":"` + d + `/."}`, "isn't a real path"},
		{`[]`, "isn't a JSON object"},
		{`null`, "isn't a JSON object"},
		{"\xff", "isn't UTF-8"},
		{``, "isn't a JSON object"},
	} {
		os.WriteFile(file, []byte(c.content), 0o644)
		m, why := Read(file, d)
		if c.why == "" && (why != "" || m != (Marker{Schema: 1, ID: 2, Path: "/work/api", Dir: d})) {
			t.Errorf("%s: %+v, %s", c.content, m, why)
		}
		if c.why != "" && !strings.Contains(why, c.why) {
			t.Errorf("%s: why %q, want %q", c.content, why, c.why)
		}
	}
	os.Remove(file)
	if _, why := Read(file, d); !strings.Contains(why, "can't be read") {
		t.Errorf("missing: %q", why)
	}
}

// The look walks up from the path, stopping at the first marker.json;
// a .koan/ without one, or a .koan that is a file, is passed over.
func TestLook(t *testing.T) {
	d := realTemp(t)
	os.MkdirAll(filepath.Join(d, "a", "b", ".koan"), 0o755)
	os.MkdirAll(filepath.Join(d, "a", DirName), 0o755)
	os.WriteFile(File(filepath.Join(d, "a")), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(d, "a", "c"), 0o755)
	os.WriteFile(filepath.Join(d, "a", "c", DirName), nil, 0o644)
	for _, p := range []string{d + "/a", d + "/a/b", d + "/a/b/x/y", d + "/a/c/file"} {
		f, ok, err := Look(p)
		if err != nil || !ok || f.Dir != d+"/a" || f.File != d+"/a/.koan/marker.json" {
			t.Errorf("Look(%s) = %+v, %v, %v", p, f, ok, err)
		}
	}
	if _, ok, err := Look(d + "/x"); ok || err != nil {
		t.Errorf("none: %v %v", ok, err)
	}
}

func TestWriteRemove(t *testing.T) {
	d := realTemp(t)
	m := Marker{Schema: 1, ID: 2, Path: "/work", Dir: d}
	os.Mkdir(filepath.Join(d, DirName), 0o755)
	os.WriteFile(filepath.Join(d, DirName, TempName), []byte("partial"), 0o644)
	if err := Write(d, m); err != nil {
		t.Fatal(err)
	}
	names, ok, err := Contents(d)
	if err != nil || !ok || !slices.Equal(names, []string{IgnoreName, FileName}) {
		t.Fatalf("contents %q %v %v", names, ok, err)
	}
	if b, _ := os.ReadFile(File(d)); string(b) != `{"schema":1,"id":2,"path":"/work","dir":"`+d+`"}`+"\n" {
		t.Errorf("marker.json %s", b)
	}
	if got, why := Read(File(d), d); why != "" || got != m {
		t.Errorf("read back %+v %s", got, why)
	}
	if Foreign([]string{IgnoreName, FileName, TempName, "x"})[0] != "x" {
		t.Errorf("foreign")
	}
	removed, err := Remove(d, names)
	if err != nil || !removed {
		t.Fatalf("remove %v %v", removed, err)
	}
	if _, ok, _ := Contents(d); ok {
		t.Errorf(".koan left")
	}
}
