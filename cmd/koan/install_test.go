package main_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/koan/internal/testrun"
)

// installer runs scripts/install.sh with HOME, CLAUDE_CONFIG_DIR and
// XDG_CONFIG_HOME in a scratch directory, and a PATH of bin alone, which
// holds the built koan, jq and the commands the script runs, so no real
// claude or opencode is found unless a test puts a stand-in in bin.
type installer struct {
	home, claude, xdg, bin string
}

func newInstaller(t *testing.T) *installer {
	t.Helper()
	if _, err := os.Stat("/bin/bash"); err != nil {
		t.Skip("install.sh runs under /bin/bash")
	}
	jq, err := exec.LookPath("jq")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("install.sh needs jq")
		}
		t.Skip("install.sh needs jq")
	}
	dir := t.TempDir()
	in := &installer{
		home:   filepath.Join(dir, "home"),
		claude: filepath.Join(dir, "home", "claude-config"),
		xdg:    filepath.Join(dir, "home", "xdg"),
		bin:    filepath.Join(dir, "bin"),
	}
	for _, d := range []string{in.home, in.bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	in.link(t, "koan", testrun.Binary(t))
	in.link(t, "jq", jq)
	for _, name := range []string{"cp", "date", "dirname", "ln", "mkdir", "mv", "rm"} {
		path, err := exec.LookPath(name)
		if err != nil {
			t.Fatal(err)
		}
		in.link(t, name, path)
	}
	return in
}

func (in *installer) link(t *testing.T, name, target string) {
	t.Helper()
	if err := os.Symlink(target, filepath.Join(in.bin, name)); err != nil {
		t.Fatal(err)
	}
}

// stub puts a script named name in bin.
func (in *installer) stub(t *testing.T, name, script string) {
	t.Helper()
	path := filepath.Join(in.bin, name)
	os.Remove(path)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// run runs install.sh under macOS's bash where it is /bin/bash, 3.2.
func (in *installer) run(t *testing.T, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("/bin/bash", append([]string{filepath.Join(testrun.Repo(), "scripts", "install.sh")}, args...)...)
	cmd.Dir = in.home
	cmd.Env = []string{
		"HOME=" + in.home,
		"CLAUDE_CONFIG_DIR=" + in.claude,
		"XDG_CONFIG_HOME=" + in.xdg,
		"PATH=" + in.bin,
		"LC_ALL=C",
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if exit, ok := err.(*exec.ExitError); ok {
		return out.String(), exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return out.String(), 0
}

func (in *installer) settings() string { return filepath.Join(in.claude, "settings.json") }
func (in *installer) ocJSON() string   { return filepath.Join(in.xdg, "opencode", "opencode.json") }
func (in *installer) ocLink() string   { return filepath.Join(in.xdg, "opencode", "skills", "koan") }

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// decode reads a JSON object from a file. Key order is lost: bashKeys
// checks it where it matters.
func decode(t *testing.T, path string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(readFile(t, path)), &v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return v
}

// bashKeys is the order of the rules in opencode.json's permission.bash.
func bashKeys(t *testing.T, path string) []string {
	t.Helper()
	out, err := exec.Command("jq", "-r", ".permission.bash | keys_unsorted[]", path).Output()
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return strings.Fields(strings.ReplaceAll(string(out), " ", "_"))
}

// backups is the backups made of path.
func backups(t *testing.T, path string) []string {
	t.Helper()
	m, err := filepath.Glob(path + ".bak.*")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

var (
	koanAllow = []any{"Bash(koan:*)", "Bash(jq:*)"}
	koanAsk   = []any{"Bash(koan init:*)", "Bash(koan task delete:*)", "Bash(koan folder delete:*)", "Bash(koan repair:*)"}
	ocKoan    = []string{"koan_*", "jq_*", "koan_init*", "koan_task_delete_*", "koan_folder_delete_*", "koan_repair*"}
)

func TestInstallShellcheck(t *testing.T) {
	if _, err := exec.LookPath("shellcheck"); err != nil {
		t.Skip("no shellcheck")
	}
	out, err := exec.Command("shellcheck", filepath.Join(testrun.Repo(), "scripts", "install.sh")).CombinedOutput()
	if err != nil {
		t.Fatalf("shellcheck: %v\n%s", err, out)
	}
}

func TestInstallFresh(t *testing.T) {
	in := newInstaller(t)
	out, code := in.run(t, "--claude", "--opencode")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	want := map[string]any{"permissions": map[string]any{"allow": koanAllow, "ask": koanAsk}}
	if got := decode(t, in.settings()); !reflect.DeepEqual(got, want) {
		t.Errorf("settings.json: %v, want %v", got, want)
	}
	oc := decode(t, in.ocJSON())
	if oc["$schema"] != "https://opencode.ai/config.json" {
		t.Errorf("opencode.json: %v", oc)
	}
	if got := bashKeys(t, in.ocJSON()); !slices.Equal(got, ocKoan) {
		t.Errorf("opencode rules %v, want %v", got, ocKoan)
	}
	bash := oc["permission"].(map[string]any)["bash"].(map[string]any)
	for k, v := range map[string]string{"koan *": "allow", "jq *": "allow", "koan init*": "ask",
		"koan task delete *": "ask", "koan folder delete *": "ask", "koan repair*": "ask"} {
		if bash[k] != v {
			t.Errorf("opencode rule %q is %v, want %s", k, bash[k], v)
		}
	}
	target, err := filepath.EvalSymlinks(in.ocLink())
	if err != nil {
		t.Fatal(err)
	}
	skill, _ := filepath.EvalSymlinks(filepath.Join(testrun.Repo(), "claude", "skills", "koan"))
	if target != skill {
		t.Errorf("skill link points at %s, want %s", target, skill)
	}
	if b := backups(t, in.settings()); len(b) != 0 {
		t.Errorf("backed up files that weren't there: %v", b)
	}
	if !strings.Contains(out, "koan has no usable root yet") || !strings.Contains(out, "koan init") {
		t.Errorf("no word of koan init with no root:\n%s", out)
	}
	if _, err := os.Lstat(filepath.Join(in.home, "koan-root")); !os.IsNotExist(err) {
		t.Errorf("install made a root: %v", err)
	}

	// A rerun changes nothing and backs nothing up.
	settings, ocjson := readFile(t, in.settings()), readFile(t, in.ocJSON())
	out, code = in.run(t, "--claude", "--opencode")
	if code != 0 || !strings.Contains(out, "Nothing changed.") || !strings.Contains(out, "already up to date") {
		t.Errorf("rerun: exit %d:\n%s", code, out)
	}
	if readFile(t, in.settings()) != settings || readFile(t, in.ocJSON()) != ocjson {
		t.Error("rerun changed the files")
	}
	if b := append(backups(t, in.settings()), backups(t, in.ocJSON())...); len(b) != 0 {
		t.Errorf("rerun backed up: %v", b)
	}

	// Uninstalling takes it all out again.
	out, code = in.run(t, "--uninstall")
	if code != 0 {
		t.Fatalf("uninstall: exit %d:\n%s", code, out)
	}
	if got := decode(t, in.settings()); len(got) != 0 {
		t.Errorf("settings.json after uninstall: %v", got)
	}
	if got := decode(t, in.ocJSON()); !reflect.DeepEqual(got, map[string]any{"$schema": "https://opencode.ai/config.json"}) {
		t.Errorf("opencode.json after uninstall: %v", got)
	}
	if _, err := os.Lstat(in.ocLink()); !os.IsNotExist(err) {
		t.Errorf("skill link still there: %v", err)
	}
	if len(backups(t, in.settings())) != 1 || len(backups(t, in.ocJSON())) != 1 {
		t.Errorf("uninstall made no backup, or more than one:\n%s", out)
	}
	out, code = in.run(t, "--uninstall")
	if code != 0 || !strings.Contains(out, "Nothing changed.") {
		t.Errorf("second uninstall: exit %d:\n%s", code, out)
	}
}

// The user's own settings stay, and are backed up before anything changes;
// the original koan's rule for koan migrate goes.
func TestInstallKeepsTheUsersSettings(t *testing.T) {
	in := newInstaller(t)
	settings := `{"model": "opus", "permissions": {"allow": ["Bash(ls:*)", "Bash(koan:*)"], "ask": ["Bash(koan migrate:*)", "Bash(rm:*)"], "deny": ["Bash(curl:*)"]}}` + "\n"
	writeFile(t, in.settings(), settings)
	ocjson := `{"model": "x", "permission": {"edit": "ask", "bash": "ask"}}` + "\n"
	writeFile(t, in.ocJSON(), ocjson)
	out, code := in.run(t, "--claude", "--opencode")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	want := map[string]any{"model": "opus", "permissions": map[string]any{
		"allow": []any{"Bash(ls:*)", "Bash(koan:*)", "Bash(jq:*)"},
		"ask":   append([]any{"Bash(rm:*)"}, koanAsk...),
		"deny":  []any{"Bash(curl:*)"}}}
	if got := decode(t, in.settings()); !reflect.DeepEqual(got, want) {
		t.Errorf("settings.json: %v\nwant %v", got, want)
	}
	if b := backups(t, in.settings()); len(b) != 1 || readFile(t, b[0]) != settings {
		t.Errorf("settings.json backups %v don't hold the original", b)
	}
	if b := backups(t, in.ocJSON()); len(b) != 1 || readFile(t, b[0]) != ocjson {
		t.Errorf("opencode.json backups %v don't hold the original", b)
	}
	oc := decode(t, in.ocJSON())
	if oc["model"] != "x" || oc["permission"].(map[string]any)["edit"] != "ask" {
		t.Errorf("opencode.json lost the user's settings: %v", oc)
	}
	if got := bashKeys(t, in.ocJSON()); !slices.Equal(got, append([]string{"*"}, ocKoan...)) {
		t.Errorf("opencode rules %v: a string bash should become the \"*\" rule, before koan's", got)
	}

	// koan's rules written before the user's move to the end, the
	// original's migrate rule goes, and a rule of the user's that matches
	// koan is warned about.
	writeFile(t, in.ocJSON(), `{"permission": {"bash": {"koan repair*": "ask", "koan migrate*": "ask", "git *": "allow", "koan show*": "deny"}}}`)
	out, code = in.run(t, "--opencode")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if got := bashKeys(t, in.ocJSON()); !slices.Equal(got, append([]string{"git_*", "koan_show*"}, ocKoan...)) {
		t.Errorf("opencode rules %v", got)
	}
	if !strings.Contains(out, "koan show*") || !strings.Contains(out, "warning") {
		t.Errorf("no warning of the user's koan rule:\n%s", out)
	}

	// Uninstalling leaves only the user's.
	out, code = in.run(t, "--uninstall", "--claude", "--opencode")
	if code != 0 {
		t.Fatalf("uninstall: exit %d:\n%s", code, out)
	}
	want = map[string]any{"model": "opus", "permissions": map[string]any{
		"allow": []any{"Bash(ls:*)"}, "ask": []any{"Bash(rm:*)"}, "deny": []any{"Bash(curl:*)"}}}
	if got := decode(t, in.settings()); !reflect.DeepEqual(got, want) {
		t.Errorf("settings.json after uninstall: %v\nwant %v", got, want)
	}
	if got := bashKeys(t, in.ocJSON()); !slices.Equal(got, []string{"git_*", "koan_show*"}) {
		t.Errorf("opencode rules after uninstall: %v", got)
	}
}

// Files install.sh must not touch: it leaves them as they are and says so.
func TestInstallRefuses(t *testing.T) {
	t.Run("opencode.jsonc", func(t *testing.T) {
		in := newInstaller(t)
		jsonc := "{\n  // mine\n  \"model\": \"x\",\n}\n"
		writeFile(t, filepath.Join(in.xdg, "opencode", "opencode.jsonc"), jsonc)
		out, code := in.run(t, "--opencode")
		if code != 0 {
			t.Fatalf("exit %d:\n%s", code, out)
		}
		if readFile(t, filepath.Join(in.xdg, "opencode", "opencode.jsonc")) != jsonc {
			t.Error("opencode.jsonc changed")
		}
		if _, err := os.Lstat(in.ocJSON()); !os.IsNotExist(err) {
			t.Error("made an opencode.json beside the opencode.jsonc")
		}
		if !strings.Contains(out, "by hand") || !strings.Contains(out, `"koan repair*": "ask",`) {
			t.Errorf("no rules to add by hand:\n%s", out)
		}
		if strings.Contains(out, "koan migrate*\": \"ask\",") {
			t.Errorf("tells the user to add the migrate rule:\n%s", out)
		}
		// The skill still goes in.
		if _, err := os.Lstat(in.ocLink()); err != nil {
			t.Errorf("no skill link: %v", err)
		}
	})
	t.Run("opencode.json not JSON", func(t *testing.T) {
		in := newInstaller(t)
		bad := "{\"model\": \"x\",}\n"
		writeFile(t, in.ocJSON(), bad)
		out, code := in.run(t, "--opencode")
		if code != 0 {
			t.Fatalf("exit %d:\n%s", code, out)
		}
		if readFile(t, in.ocJSON()) != bad || len(backups(t, in.ocJSON())) != 0 {
			t.Error("opencode.json changed")
		}
		if !strings.Contains(out, "isn't plain JSON") {
			t.Errorf("no word of why:\n%s", out)
		}
	})
	t.Run("settings.json not JSON", func(t *testing.T) {
		in := newInstaller(t)
		bad := "{\"permissions\": \n"
		writeFile(t, in.settings(), bad)
		out, code := in.run(t, "--claude", "--opencode")
		if code != 1 || !strings.Contains(out, "isn't valid JSON") {
			t.Errorf("exit %d:\n%s", code, out)
		}
		if readFile(t, in.settings()) != bad || len(backups(t, in.settings())) != 0 {
			t.Error("settings.json changed")
		}
		if _, err := os.Lstat(filepath.Join(in.xdg, "opencode")); !os.IsNotExist(err) {
			t.Error("changed OpenCode's config before refusing")
		}
	})
	t.Run("a koan skill of the user's", func(t *testing.T) {
		for _, dir := range []string{".claude/skills/koan", "claude-config/skills/koan"} {
			in := newInstaller(t)
			mine := filepath.Join(in.home, dir, "SKILL.md")
			writeFile(t, mine, "mine\n")
			out, code := in.run(t, "--claude", "--opencode")
			if code != 1 || !strings.Contains(out, "Nothing was changed") {
				t.Errorf("%s: exit %d:\n%s", dir, code, out)
			}
			if readFile(t, mine) != "mine\n" {
				t.Errorf("%s changed", dir)
			}
			for _, p := range []string{in.settings(), filepath.Join(in.xdg, "opencode")} {
				if _, err := os.Lstat(p); !os.IsNotExist(err) {
					t.Errorf("%s: made %s before refusing", dir, p)
				}
			}
			// Uninstalling leaves it in place too, and goes on.
			out, code = in.run(t, "--uninstall", "--claude")
			if code != 0 || readFile(t, mine) != "mine\n" {
				t.Errorf("%s: uninstall: exit %d:\n%s", dir, code, out)
			}
		}
	})
	t.Run("the skill link's place taken", func(t *testing.T) {
		in := newInstaller(t)
		mine := filepath.Join(in.ocLink(), "SKILL.md")
		writeFile(t, mine, "mine\n")
		out, code := in.run(t, "--opencode")
		if code != 1 || !strings.Contains(out, "left alone") {
			t.Errorf("exit %d:\n%s", code, out)
		}
		if readFile(t, mine) != "mine\n" {
			t.Error("the user's skill changed")
		}
		// The rules still go in.
		if got := bashKeys(t, in.ocJSON()); !slices.Equal(got, ocKoan) {
			t.Errorf("opencode rules %v", got)
		}
		out, code = in.run(t, "--uninstall", "--opencode")
		if code != 0 || readFile(t, mine) != "mine\n" || !strings.Contains(out, "left in place") {
			t.Errorf("uninstall: exit %d:\n%s", code, out)
		}
	})
}

// An old link of an earlier install.sh, in ~/.claude/skills, is removed.
func TestInstallRemovesTheOldLink(t *testing.T) {
	in := newInstaller(t)
	old := filepath.Join(in.home, ".claude", "skills", "koan")
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(testrun.Repo(), "claude", "skills", "koan"), old); err != nil {
		t.Fatal(err)
	}
	out, code := in.run(t, "--claude")
	if code != 0 || !strings.Contains(out, "removed the old link") {
		t.Errorf("exit %d:\n%s", code, out)
	}
	if _, err := os.Lstat(old); !os.IsNotExist(err) {
		t.Errorf("old link still there: %v", err)
	}
}

func TestInstallPicksAgents(t *testing.T) {
	in := newInstaller(t)
	out, code := in.run(t)
	if code != 0 || !strings.Contains(out, "Nothing to do.") {
		t.Errorf("no agents on PATH: exit %d:\n%s", code, out)
	}
	if _, err := os.Lstat(in.claude); !os.IsNotExist(err) {
		t.Error("made Claude Code's config with no claude on PATH")
	}

	// claude on PATH, without the plugin: its rules, and word of the plugin.
	in.stub(t, "claude", `[ "$1 $2 $3" = "plugin list --json" ] && echo '[]'`)
	out, code = in.run(t)
	if code != 0 || !strings.Contains(out, "claude plugin install koan@koan") || !strings.Contains(out, "opencode: skipped") {
		t.Errorf("claude on PATH: exit %d:\n%s", code, out)
	}
	if _, err := os.Lstat(in.settings()); err != nil {
		t.Errorf("no settings.json: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(in.xdg, "opencode")); !os.IsNotExist(err) {
		t.Error("set up OpenCode with no opencode on PATH")
	}

	// With the plugin, no word of it.
	in.stub(t, "claude", `[ "$1 $2 $3" = "plugin list --json" ] && echo '[{"id": "koan@koan", "enabled": true}]'`)
	if out, _ = in.run(t); strings.Contains(out, "plugin install") {
		t.Errorf("plugin installed, but:\n%s", out)
	}

	// Uninstalling picks what has something to clean up.
	in.stub(t, "opencode", "true")
	if out, _ = in.run(t); !strings.Contains(out, "linked the skill") {
		t.Errorf("opencode on PATH:\n%s", out)
	}
	os.Remove(filepath.Join(in.bin, "claude"))
	os.Remove(filepath.Join(in.bin, "opencode"))
	out, code = in.run(t, "--uninstall")
	if code != 0 || !strings.Contains(out, "removed the skill link") || !strings.Contains(out, "claude: updated") {
		t.Errorf("uninstall: exit %d:\n%s", code, out)
	}
}

func TestInstallNeeds(t *testing.T) {
	in := newInstaller(t)
	os.Remove(filepath.Join(in.bin, "koan"))
	out, code := in.run(t, "--claude")
	if code != 1 || !strings.Contains(out, "go install github.com/phansen314/koan/cmd/koan@latest") {
		t.Errorf("no koan: exit %d:\n%s", code, out)
	}
	if _, err := os.Lstat(in.claude); !os.IsNotExist(err) {
		t.Error("changed something with no koan")
	}
	// Uninstalling needs no koan.
	if out, code = in.run(t, "--uninstall", "--claude"); code != 0 {
		t.Errorf("uninstall with no koan: exit %d:\n%s", code, out)
	}
	os.Remove(filepath.Join(in.bin, "jq"))
	in.link(t, "koan", testrun.Binary(t))
	if out, code = in.run(t, "--claude"); code != 1 || !strings.Contains(out, "needs jq") {
		t.Errorf("no jq: exit %d:\n%s", code, out)
	}
	if out, code = in.run(t, "--bogus"); code != 2 || !strings.Contains(out, "usage") {
		t.Errorf("--bogus: exit %d:\n%s", code, out)
	}
}

// What install.sh says of the root and the binary it finds.
func TestInstallSaysWhatIsLeft(t *testing.T) {
	in := newInstaller(t)
	writeFile(t, filepath.Join(in.home, "koan-root", "@koan.json"), "{}\n")
	out, code := in.run(t, "--claude")
	if code != 0 || !strings.Contains(out, "root of the original koan") || !strings.Contains(out, "mv ~/koan-root ~/koan-root.old && koan init") {
		t.Errorf("old root: exit %d:\n%s", code, out)
	}
	os.RemoveAll(filepath.Join(in.home, "koan-root"))
	r := testrun.Run{Home: in.home}.Koan(t, "init")
	if !r.OK() {
		t.Fatal(r.Line())
	}
	if out, _ = in.run(t, "--claude"); strings.Contains(out, "koan init") {
		t.Errorf("usable root, but:\n%s", out)
	}
	// The original koan printed its version without the v.
	os.Remove(filepath.Join(in.bin, "koan"))
	in.stub(t, "koan", `echo '{"ok":true,"result":{"version":"0.3.0"},"warnings":[]}'`)
	if out, _ = in.run(t, "--claude"); !strings.Contains(out, "is the original koan (0.3.0)") {
		t.Errorf("original koan on PATH:\n%s", out)
	}
}
