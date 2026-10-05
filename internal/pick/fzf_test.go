package pick

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/schematest"
)

func TestParseVersion(t *testing.T) {
	for _, tc := range []struct {
		out   string
		found string
		v     version
		ok    bool
	}{
		{"0.63.0 (397fe8e3)\n", "0.63.0", version{0, 63, 0}, true},
		{"0.74.4 (Fedora)\n", "0.74.4", version{0, 74, 4}, true},
		{"0.75.0-dev (abc)\n", "0.75.0-dev", version{0, 75, 0}, true},
		{"  1.2.3\n", "1.2.3", version{1, 2, 3}, true},
		{"0.63.0\r\n", "0.63.0", version{0, 63, 0}, true},
		{"10.0.100", "10.0.100", version{10, 0, 100}, true},
		// Doesn't parse: found is the first line as printed.
		{"", "", version{}, false},
		{"\n", "", version{}, false},
		{"0.63\n", "0.63", version{}, false},
		{"0.63.0.1 (x)\nmore\n", "0.63.0.1 (x)", version{}, false},
		{"fzf 0.63.0\n", "fzf 0.63.0", version{}, false},
		{"0.63.x\n", "0.63.x", version{}, false},
		{"0..0\n", "0..0", version{}, false},
		{"+1.0.0\n", "+1.0.0", version{}, false},
		{"0.-1.0\n", "0.-1.0", version{}, false},
		{"99999999999999999999.0.0\n", "99999999999999999999.0.0", version{}, false},
	} {
		found, v, ok := parseVersion([]byte(tc.out))
		if found != tc.found || ok != tc.ok || (ok && v != tc.v) {
			t.Errorf("%q: got (%q, %v, %v), want (%q, %v, %v)", tc.out, found, v, ok, tc.found, tc.v, tc.ok)
		}
	}
}

// fakeFzf is a System whose fzf prints out and exits with status, or, with
// startErr, cannot be started; missing leaves it off PATH.
type fakeFzf struct {
	missing  bool
	out, err string
	status   int
	startErr error
	gotEnv   []string
	gotArgs  []string
}

func (f *fakeFzf) system(environ ...string) System {
	return System{
		LookPath: func(file string) (string, error) {
			if f.missing || file != "fzf" {
				return "", errors.New("not found")
			}
			return "/bin/fzf", nil
		},
		Output: func(path string, args, env []string) ([]byte, []byte, int, error) {
			f.gotArgs, f.gotEnv = args, env
			return []byte(f.out), []byte(f.err), f.status, f.startErr
		},
		Environ: func() []string { return environ },
	}
}

func TestFindFzf(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fzf    fakeFzf
		reason UnavailableReason // "" for found
		found  string
		status int
	}{
		{"minimum", fakeFzf{out: "0.63.0 (397fe8e3)\n"}, "", "", 0},
		{"newer", fakeFzf{out: "0.74.4 (Fedora)\n"}, "", "", 0},
		{"major", fakeFzf{out: "1.0.0\n"}, "", "", 0},
		{"dev build", fakeFzf{out: "0.63.0-dev\n"}, "", "", 0},
		{"missing", fakeFzf{missing: true}, FzfMissing, "", 0},
		{"older", fakeFzf{out: "0.62.9 (x)\n"}, FzfTooOld, "0.62.9", 0},
		{"older dev", fakeFzf{out: "0.62.0-dev\n"}, FzfTooOld, "0.62.0-dev", 0},
		{"unreadable", fakeFzf{out: "fzf version 2\n"}, FzfTooOld, "fzf version 2", 0},
		{"no output", fakeFzf{}, FzfTooOld, "", 0},
		{"exits with an error", fakeFzf{err: "boom\nmore", status: 2}, FzfFailed, "", 2},
		{"can't start", fakeFzf{startErr: errors.New("exec format error")}, FzfFailed, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, e := findFzf(tc.fzf.system())
			if tc.reason == "" {
				if e != nil || path != "/bin/fzf" {
					t.Fatalf("got %q, %v", path, e)
				}
				return
			}
			if e == nil || e.Kind != errs.KindUnavailable {
				t.Fatalf("got %q, %v; want unavailable", path, e)
			}
			b, err := json.Marshal(e.Details)
			if err != nil {
				t.Fatal(err)
			}
			if ok, f := schematest.Check(t, "pick-error-details#/$defs/unavailable", b); !ok {
				t.Errorf("details rejected at %s: %s", f, b)
			}
			// actions is in the JSON exactly when fzf ran, empty or not.
			if (tc.reason == FzfFailed) != strings.Contains(string(b), `"actions":[]`) {
				t.Errorf("details %s", b)
			}
			d := e.Details.(UnavailableDetails)
			if d.Reason != tc.reason {
				t.Errorf("reason %s, want %s", d.Reason, tc.reason)
			}
			switch tc.reason {
			case FzfTooOld:
				if d.Found == nil || *d.Found != tc.found || d.Required != "0.63.0" {
					t.Errorf("details %s", b)
				}
			case FzfFailed:
				// fzf ran, before any action: actions is present, and empty.
				if d.Actions == nil || len(d.Actions) != 0 || (tc.status != 0) != (d.Status != nil) || (d.Status != nil && *d.Status != tc.status) {
					t.Errorf("details %s", b)
				}
			default:
				if d.Found != nil || d.Required != "" || d.Status != nil || d.Actions != nil {
					t.Errorf("details %s", b)
				}
			}
		})
	}
}

// The version is asked for without the person's fzf options, which could
// make fzf --version fail; the rest of the environment is passed on.
func TestFindFzfEnvironment(t *testing.T) {
	f := fakeFzf{out: "0.63.0\n"}
	if _, e := findFzf(f.system("PATH=/bin", "FZF_DEFAULT_OPTS=--bogus", "FZF_DEFAULT_OPTS_FILE=/x", "FZF_DEFAULT_OPTSX=y", "HOME=/h")); e != nil {
		t.Fatal(e)
	}
	if want := []string{"PATH=/bin", "FZF_DEFAULT_OPTSX=y", "HOME=/h"}; !slices.Equal(f.gotEnv, want) {
		t.Errorf("env %q, want %q", f.gotEnv, want)
	}
	if !slices.Equal(f.gotArgs, []string{"--version"}) {
		t.Errorf("args %q", f.gotArgs)
	}
}

// With nothing else in the environment, fzf still gets an empty one, not
// this process's.
func TestFindFzfEmptyEnvironment(t *testing.T) {
	f := fakeFzf{out: "0.63.0\n"}
	if _, e := findFzf(f.system("FZF_DEFAULT_OPTS=--bogus")); e != nil {
		t.Fatal(e)
	}
	if f.gotEnv == nil || len(f.gotEnv) != 0 {
		t.Errorf("env %#v, want empty and non-nil", f.gotEnv)
	}
}
