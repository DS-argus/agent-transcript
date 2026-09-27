package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"agent-transcript/internal/transcript"
)

const claudeStart = "Sun Sep 13 02:31:16 2026"

func claudeRegistry(pid int, id, start string) map[string]any {
	return map[string]any{"pid": pid, "sessionId": id, "procStart": start,
		"pidDomain": runtime.GOOS, "kind": "interactive", "entrypoint": "cli"}
}

func enableClaudeAdapter(t *testing.T) {
	t.Helper()
	old := adapters
	t.Cleanup(func() { adapters = old })
	adapters = append(append([]Adapter{}, old...), Adapter{
		Name: "claude", Executables: []string{"claude"}, NodeScripts: []string{"/@anthropic-ai/claude-code/cli.js"}, Locate: Resolver.locateClaude,
		Detect: recognizeClaude,
		Render: transcript.RenderClaude,
	})
}

func registeredClaude(t *testing.T, base string, pid int, id string) string {
	t.Helper()
	writeRecords(t, filepath.Join(base, "claude", "sessions", fmt.Sprintf("%d.json", pid)), claudeRegistry(pid, id, claudeStart))
	return writeRecords(t, filepath.Join(base, "claude/projects/workspace", id+".jsonl"), map[string]any{
		"type": "user", "sessionId": id, "uuid": "u", "parentUuid": nil,
		"message": map[string]any{"role": "user", "content": "User question"}})
}

func registryResolver(t *testing.T, base string, openFiles ...string) Resolver {
	r := resolver(t, base, openFiles...)
	run := r.Run
	r.Run = func(name string, args ...string) ([]byte, error) {
		if name == "ps" && len(args) == 4 && args[0] == "-p" && args[3] == "lstart=" {
			return []byte("  " + claudeStart + "\n"), nil
		}
		return run(name, args...)
	}
	return r
}

func TestClaudeRegistryWithoutOpenTranscript(t *testing.T) {
	enableClaudeAdapter(t)
	base := t.TempDir()
	path := registeredClaude(t, base, 101, "main")
	got, err := registryResolver(t, base).Locate("claude", []int{100, 101})
	if err != nil || got != (Source{"claude", path}) {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestClaudeRegistryExcludesSiblingAndReusedPID(t *testing.T) {
	enableClaudeAdapter(t)
	for _, pid := range []int{101, 200} {
		t.Run(fmt.Sprint(pid), func(t *testing.T) {
			base := t.TempDir()
			registeredClaude(t, base, pid, "main")
			if pid == 101 {
				writeRecords(t, filepath.Join(base, "claude/sessions", fmt.Sprintf("%d.json", pid)), claudeRegistry(pid, "main", "Sun Sep 13 02:00:00 2026"))
			}
			if _, err := registryResolver(t, base).Locate("claude", []int{100, 101}); err == nil {
				t.Fatal("unowned registry selected")
			}
		})
	}
}

func TestClaudeRegistryAndOpenFileAreDeduplicated(t *testing.T) {
	enableClaudeAdapter(t)
	base := t.TempDir()
	path := registeredClaude(t, base, 101, "main")
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := registryResolver(t, base, canonical).Locate("claude", []int{100, 101})
	if err != nil || got.Harness != "claude" {
		t.Fatalf("same transcript falsely ambiguous: %+v %v", got, err)
	}
}

func TestClaudeRegistryRejectsSessionMismatchAndDuplicateFiles(t *testing.T) {
	enableClaudeAdapter(t)
	base := t.TempDir()
	path := registeredClaude(t, base, 101, "main")
	writeRecords(t, path, map[string]any{"type": "user", "sessionId": "wrong", "message": map[string]any{"role": "user", "content": "not main"}})
	if _, err := registryResolver(t, base).Locate("claude", []int{100, 101}); err == nil || !strings.Contains(err.Error(), "disagree") {
		t.Fatal(err)
	}
	registeredClaude(t, base, 101, "main")
	writeRecords(t, filepath.Join(base, "claude/projects/another/main.jsonl"), map[string]any{"type": "user", "sessionId": "main", "message": map[string]any{"role": "user", "content": "duplicate"}})
	if _, err := registryResolver(t, base).Locate("claude", []int{100, 101}); err == nil || !strings.Contains(err.Error(), "2 transcript") {
		t.Fatal(err)
	}
}

func TestClaudeRegistrySessionSwitchIsDetected(t *testing.T) {
	enableClaudeAdapter(t)
	base := t.TempDir()
	registeredClaude(t, base, 101, "main")
	r := registryResolver(t, base)
	run := r.Run
	r.Run = func(name string, args ...string) ([]byte, error) {
		if name == "ps" && len(args) == 4 && args[0] == "-p" {
			writeRecords(t, filepath.Join(base, "claude/sessions/101.json"), claudeRegistry(101, "other", claudeStart))
		}
		return run(name, args...)
	}
	if _, err := r.Locate("claude", []int{100, 101}); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatal(err)
	}
}

func TestClaudeRegistryMissingTranscriptIsActionable(t *testing.T) {
	enableClaudeAdapter(t)
	base := t.TempDir()
	path := registeredClaude(t, base, 101, "main")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := registryResolver(t, base).Locate("claude", []int{100, 101}); err == nil || !strings.Contains(err.Error(), "finish a conversation turn") {
		t.Fatal(err)
	}
}

func TestProcessInspectionUsesUTCWithoutChangingParent(t *testing.T) {
	t.Setenv("TZ", "Asia/Seoul")
	t.Setenv("LC_ALL", "C")
	out, err := Run("sh", "-c", `printf '%s|%s' "$TZ" "$LC_ALL"`)
	if err != nil || string(out) != "UTC|C" {
		t.Fatalf("%q %v", out, err)
	}
	if os.Getenv("TZ") != "Asia/Seoul" {
		t.Fatal("parent environment changed")
	}
}
