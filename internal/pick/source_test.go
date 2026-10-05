package pick

import (
	"strings"
	"testing"
	"time"

	"github.com/phansen314/koan/internal/errs"
	"github.com/phansen314/koan/internal/model"
)

func TestSourceIDs(t *testing.T) {
	type run struct {
		stdout, stderr string
		timedOut       bool
	}
	for _, tc := range []struct {
		name   string
		run    run
		ids    []model.ID
		reason string
	}{
		{"list", run{`{"ok":true,"result":{"tasks":[{"id":3},{"id":1},{"id":3}]},"warnings":[]}`, "", false}, []model.ID{3, 1}, ""},
		{"one task", run{`{"ok":true,"result":{"id":2,"title":"x"},"warnings":[]}`, "noise", false}, []model.ID{2}, ""},
		{"empty", run{`{"ok":true,"result":{"tasks":[]},"warnings":[]}`, "", false}, []model.ID{}, ""},
		{"upstream failed", run{`{"ok":false,"error":{"kind":"not-found","message":"not found: folder /x"},"warnings":[]}`, "koan: not-found: …", false},
			nil, "upstream failed with not-found: not found: folder /x"},
		{"stderr", run{"", "sh: 1: ftsk: not found\nmore\n", false}, nil, "sh: 1: ftsk: not found"},
		{"no stderr", run{"nope", "", false}, nil, "not a koan envelope: "},
		{"not an ID", run{`{"ok":true,"result":{"tasks":[{"id":"x"}]},"warnings":[]}`, "", false}, nil, `not a task ID: "x"`},
		{"timed out", run{"", "", true}, nil, "timed out after 10s"},
	} {
		var gotEnv []string
		env := Env{Sys: System{
			Environ: func() []string { return []string{"A=1", SessionVar + "=/s"} },
			RunSource: func(command string, e []string, limit time.Duration) ([]byte, []byte, bool, error) {
				if command != "cmd" || limit != sourceLimit {
					t.Errorf("%s: ran %q, %v", tc.name, command, limit)
				}
				gotEnv = e
				return []byte(tc.run.stdout), []byte(tc.run.stderr), tc.run.timedOut, nil
			},
		}}
		ids, reason := sourceIDs(env, "cmd", sourceLimit)
		if !strings.HasPrefix(reason, tc.reason) || (tc.reason == "") != (reason == "") || len(ids) != len(tc.ids) {
			t.Errorf("%s: %v, %q", tc.name, ids, reason)
		}
		for i := range tc.ids {
			if i < len(ids) && ids[i] != tc.ids[i] {
				t.Errorf("%s: %v", tc.name, ids)
			}
		}
		if len(gotEnv) != 1 || gotEnv[0] != "A=1" {
			t.Errorf("%s: env %q", tc.name, gotEnv)
		}
	}
}

// The real runner: sh -c, stdin from /dev/null, stderr apart; a limit
// kills the command's whole process group.
func TestRunSource(t *testing.T) {
	out, errOut, timedOut, err := runSource(`cat; echo out; echo err >&2; exit 3`, nil, 0)
	if string(out) != "out\n" || string(errOut) != "err\n" || timedOut || err != nil {
		t.Errorf("%q %q %v %v", out, errOut, timedOut, err)
	}
	start := time.Now()
	out, _, timedOut, err = runSource(`sleep 30 & echo started; sleep 30`, nil, 300*time.Millisecond)
	if !timedOut || err != nil || string(out) != "started\n" || time.Since(start) > 5*time.Second {
		t.Errorf("limit: %q %v %v after %v", out, timedOut, err, time.Since(start))
	}
}

// A live source through pick whole: its first run's IDs are the
// candidates, a failed first run is invalid-input at /source before fzf,
// and every reload runs it again, a failure leaving the list as it was.
func TestLiveSource(t *testing.T) {
	tr := newTestTree(t)
	for _, title := range []string{"one", "two", "three"} {
		tr.run("create", map[string]any{"title": title})
	}
	tr.run("done", map[string]any{"id": 3})
	listed := `{"ok":true,"result":{"tasks":[{"id":3},{"id":1},{"id":9}]},"warnings":[]}`
	runs := 0
	tr.source = func(command string, limit time.Duration) (string, string, bool) {
		runs++
		switch {
		case runs == 1 && limit != 0, runs > 1 && limit != sourceLimit:
			t.Errorf("run %d: limit %v", runs, limit)
		}
		return listed, "", false
	}
	lineKeys := func(helper func(...string) string) string {
		var ks []string
		for _, l := range strings.Split(strings.TrimSuffix(helper("lines"), "\n"), "\n") {
			k, _, _ := strings.Cut(l, "\t")
			ks = append(ks, k)
		}
		return strings.Join(ks, " ")
	}
	tr.pick(map[string]any{"source": "my source"}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		// Scope all; pick's own order; 9 not found.
		if ks, h := lineKeys(helper), helper("text", "header"); ks != "1@/ 3@/" {
			t.Errorf("first lines %s, header %q", ks, h)
		}
		helper("command", "")
		if h := helper("text", "header"); !strings.HasPrefix(h, "[cmd]\n/ · live source · 1 given ID not found\n") {
			t.Errorf("header %q", h)
		}
		listed = `{"ok":true,"result":{"tasks":[{"id":2}]},"warnings":[]}`
		helper("act", "r")
		if ks := lineKeys(helper); ks != "2@/" {
			t.Errorf("after r: %s", ks)
		}
		listed = `{"ok":false,"error":{"kind":"busy","message":"another write holds the lock"},"warnings":[]}`
		if got := helper("act", "r"); got != "clear-selection+transform-footer('/bin/koan' __pick text 'footer')" {
			t.Errorf("failed r printed %q", got)
		}
		if f, ks := helper("text", "footer"), lineKeys(helper); f != "✗ source: upstream failed with busy: another write holds the lock" || ks != "2@/" {
			t.Errorf("failed: footer %q, lines %s", f, ks)
		}
		helper("quit")
	}})

	tr.source = func(string, time.Duration) (string, string, bool) { return "", "ftsk: command not found\n", false }
	out, line := tr.pick(map[string]any{"source": "ftsk list"}, fzfDoes{do: func(t *testing.T, _ func(...string) string) {
		t.Error("fzf ran")
	}})
	if out.OK || out.Error.Kind != errs.KindInvalidInput || !strings.Contains(string(line), `"problems":[{"field":"/source","reason":"ftsk: command not found"}]`) {
		t.Errorf("%s", line)
	}
}
