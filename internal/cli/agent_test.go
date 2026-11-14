package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// harness runs eoy in a fresh project directory with a fresh home directory.
type harness struct {
	t         *testing.T
	cwd, home string
}

func newHarness(t *testing.T) *harness {
	return &harness{t: t}
}

func (h *harness) run(args ...string) (string, error) {
	h.t.Helper()
	if h.cwd == "" {
		h.cwd = h.t.TempDir()
	}
	if h.home == "" {
		h.home = h.t.TempDir()
	}
	h.t.Setenv("HOME", h.home)
	h.t.Chdir(h.cwd)
	code, out, errOut := run(h.t, Resources(), args...)
	if code != ExitOK {
		return out, errorString(errOut)
	}
	return out, nil
}

type errorString string

func (e errorString) Error() string { return string(e) }

func (h *harness) mustRun(args ...string) string {
	h.t.Helper()
	out, err := h.run(args...)
	if err != nil {
		h.t.Fatalf("%v: %v", args, err)
	}
	return out
}

func TestPrimePrintsGuide(t *testing.T) {
	out := newHarness(t).mustRun("prime")
	for _, want := range []string{"eoy get evidence", "-o json", "--force", "eoy create evidence -f", "eoy create note", "eoy sync prs", "eoy VERB RESOURCE --help"} {
		if !strings.Contains(out, want) {
			t.Errorf("prime output has no %q:\n%s", want, out)
		}
	}
}

func readSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return m
}

// primeHookCount counts the command hooks that run "eoy prime" at session start.
func primeHookCount(t *testing.T, settings map[string]any) int {
	t.Helper()
	hooks, _ := settings["hooks"].(map[string]any)
	entries, _ := hooks["SessionStart"].([]any)
	n := 0
	for _, e := range entries {
		entry, _ := e.(map[string]any)
		list, _ := entry["hooks"].([]any)
		for _, hk := range list {
			m, _ := hk.(map[string]any)
			if m["type"] == "command" && m["command"] == "eoy prime" {
				n++
			}
		}
	}
	return n
}

func TestInitCreatesLocalSettings(t *testing.T) {
	h := newHarness(t)
	out := h.mustRun("init")
	local := filepath.Join(h.cwd, ".claude", "settings.local.json")
	if !strings.Contains(out, "Added SessionStart hook to "+local) {
		t.Fatalf("init output:\n%s", out)
	}
	if n := primeHookCount(t, readSettings(t, local)); n != 1 {
		t.Fatalf("got %d prime hooks, want 1", n)
	}
	if _, err := os.Stat(filepath.Join(h.cwd, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("settings.json exists, want only settings.local.json: %v", err)
	}
}

func TestInitSharedWritesSettingsJSON(t *testing.T) {
	h := newHarness(t)
	h.mustRun("init", "--shared")
	if n := primeHookCount(t, readSettings(t, filepath.Join(h.cwd, ".claude", "settings.json"))); n != 1 {
		t.Fatalf("got %d prime hooks, want 1", n)
	}
	if _, err := os.Stat(filepath.Join(h.cwd, ".claude", "settings.local.json")); !os.IsNotExist(err) {
		t.Fatalf("settings.local.json exists: %v", err)
	}
}

func TestInitIsIdempotent(t *testing.T) {
	h := newHarness(t)
	h.mustRun("init")
	out := h.mustRun("init")
	if !strings.Contains(out, "already present") {
		t.Fatalf("second init output:\n%s", out)
	}
	if n := primeHookCount(t, readSettings(t, filepath.Join(h.cwd, ".claude", "settings.local.json"))); n != 1 {
		t.Fatalf("got %d prime hooks, want 1", n)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInitKeepsExistingSettings(t *testing.T) {
	h := newHarness(t)
	h.cwd = t.TempDir()
	path := filepath.Join(h.cwd, ".claude", "settings.local.json")
	writeFile(t, path, `{
  "permissions": {"allow": ["Bash(go test:*)"]},
  "hooks": {
    "SessionStart": [{"matcher": "startup", "hooks": [{"type": "command", "command": "other"}]}],
    "Stop": [{"hooks": [{"type": "command", "command": "bye"}]}]
  }
}`)
	h.mustRun("init")
	got := readSettings(t, path)
	if _, ok := got["permissions"]; !ok {
		t.Fatalf("permissions lost: %v", got)
	}
	hooks := got["hooks"].(map[string]any)
	if _, ok := hooks["Stop"]; !ok {
		t.Fatalf("Stop hook lost: %v", hooks)
	}
	b, _ := json.Marshal(hooks["SessionStart"])
	if !strings.Contains(string(b), `"other"`) {
		t.Fatalf("other SessionStart hook lost: %s", b)
	}
	if n := primeHookCount(t, got); n != 1 {
		t.Fatalf("got %d prime hooks, want 1", n)
	}
}

func TestInitRefusesBadJSON(t *testing.T) {
	h := newHarness(t)
	h.cwd = t.TempDir()
	path := filepath.Join(h.cwd, ".claude", "settings.local.json")
	const bad = `{"hooks": `
	writeFile(t, path, bad)
	if _, err := h.run("init"); err == nil {
		t.Fatal("init with bad JSON succeeded")
	}
	b, _ := os.ReadFile(path)
	if string(b) != bad {
		t.Fatalf("file changed to %q", b)
	}
}

func TestInitRefusesWrongHookType(t *testing.T) {
	h := newHarness(t)
	h.cwd = t.TempDir()
	path := filepath.Join(h.cwd, ".claude", "settings.local.json")
	const odd = `{"hooks": {"SessionStart": "eoy prime"}}`
	writeFile(t, path, odd)
	if _, err := h.run("init"); err == nil {
		t.Fatal("init with a string SessionStart succeeded")
	}
	b, _ := os.ReadFile(path)
	if string(b) != odd {
		t.Fatalf("file changed to %q", b)
	}
}

func TestInitRefusesUserLevel(t *testing.T) {
	h := newHarness(t)
	h.home = t.TempDir()
	h.cwd = h.home
	_, err := h.run("init")
	if err == nil || !strings.Contains(err.Error(), "user-level") {
		t.Fatalf("init in home directory: err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.home, ".claude")); !os.IsNotExist(err) {
		t.Fatalf(".claude created in home: %v", err)
	}
}

func TestInitRefusesUserLevelThroughSymlink(t *testing.T) {
	h := newHarness(t)
	h.home = t.TempDir()
	link := filepath.Join(t.TempDir(), "home-link")
	if err := os.Symlink(h.home, link); err != nil {
		t.Skip(err)
	}
	h.cwd = link
	if _, err := h.run("init"); err == nil {
		t.Fatal("init through a symlink to home succeeded")
	}
}
