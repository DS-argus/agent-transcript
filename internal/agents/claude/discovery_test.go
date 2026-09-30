package claude

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"agent-transcript/internal/agents"
)

const claudeStart = "Sun Sep 13 02:31:16 2026"

func claudeRegistry(pid int, id, start string) map[string]any {
	return map[string]any{"pid": pid, "sessionId": id, "procStart": start, "pidDomain": runtime.GOOS, "kind": "interactive", "entrypoint": "cli"}
}

func registeredClaude(t *testing.T, base string, pid int, id string) string {
	t.Helper()
	writeRecords(t, filepath.Join(base, "claude", "sessions", fmt.Sprintf("%d.json", pid)), claudeRegistry(pid, id, claudeStart))
	path := writeRecords(t, filepath.Join(base, "claude/projects/workspace", id+".jsonl"), map[string]any{
		"type": "user", "sessionId": id, "uuid": "u", "parentUuid": nil,
		"message": map[string]any{"role": "user", "content": "User question"}})
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func registryResolver(t *testing.T, base string) Resolver {
	t.Helper()
	return Resolver{Dir: filepath.Join(base, "claude"), Run: func(name string, args ...string) ([]byte, error) {
		if name != "ps" || len(args) != 4 || args[0] != "-p" || args[2] != "-o" || args[3] != "lstart=" {
			t.Fatalf("unexpected process inspection: %s %v", name, args)
		}
		return []byte("  " + claudeStart + "\n"), nil
	}}
}

func TestClaudeRegistryWithoutOpenTranscript(t *testing.T) {
	base := t.TempDir()
	path := registeredClaude(t, base, 101, "main")
	got, err := registryResolver(t, base).Locate([]int{100, 101})
	if err != nil || got != (agents.Source{Harness: "claude", Path: path}) {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestClaudeRegistryExcludesSiblingAndReusedPID(t *testing.T) {
	for _, pid := range []int{101, 200} {
		t.Run(fmt.Sprint(pid), func(t *testing.T) {
			base := t.TempDir()
			registeredClaude(t, base, pid, "main")
			if pid == 101 {
				writeRecords(t, filepath.Join(base, "claude/sessions", fmt.Sprintf("%d.json", pid)), claudeRegistry(pid, "main", "Sun Sep 13 02:00:00 2026"))
			}
			if _, err := registryResolver(t, base).Locate([]int{100, 101}); err == nil {
				t.Fatal("unowned registry selected")
			}
		})
	}
}

func TestClaudeNoOpenFileFallback(t *testing.T) {
	base := t.TempDir()
	path := registeredClaude(t, base, 101, "main")
	if err := os.Remove(filepath.Join(base, "claude/sessions/101.json")); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := registryResolver(t, base).Locate([]int{101}); err == nil {
		t.Fatal("transcript without registry selected")
	}
}

func TestClaudeRegistryDuplicateOwnerPathsAreDeduplicated(t *testing.T) {
	base := t.TempDir()
	path := registeredClaude(t, base, 101, "main")
	writeRecords(t, filepath.Join(base, "claude/sessions/102.json"), claudeRegistry(102, "main", claudeStart))
	got, err := registryResolver(t, base).Locate([]int{101, 102, 101})
	if err != nil || got.Path != path {
		t.Fatalf("same transcript falsely ambiguous: %+v %v", got, err)
	}
}

func TestClaudeRegistryRejectsSessionMismatchAndDuplicateFiles(t *testing.T) {
	base := t.TempDir()
	path := registeredClaude(t, base, 101, "main")
	writeRecords(t, path, map[string]any{"type": "user", "sessionId": "wrong", "message": map[string]any{"role": "user", "content": "not main"}})
	if _, err := registryResolver(t, base).Locate([]int{100, 101}); err == nil || !strings.Contains(err.Error(), "disagree") {
		t.Fatal(err)
	}
	registeredClaude(t, base, 101, "main")
	writeRecords(t, filepath.Join(base, "claude/projects/another/main.jsonl"), map[string]any{"type": "user", "sessionId": "main", "message": map[string]any{"role": "user", "content": "duplicate"}})
	if _, err := registryResolver(t, base).Locate([]int{100, 101}); err == nil || !strings.Contains(err.Error(), "2 transcript") {
		t.Fatal(err)
	}
}

func TestClaudeRegistrySessionSwitchIsDetected(t *testing.T) {
	for _, field := range []string{"sessionId", "pid", "procStart", "kind", "entrypoint", "pidDomain", "deleted", "corrupt"} {
		t.Run(field, func(t *testing.T) {
			base := t.TempDir()
			registeredClaude(t, base, 101, "main")
			r := registryResolver(t, base)
			run := r.Run
			r.Run = func(name string, args ...string) ([]byte, error) {
				path := filepath.Join(base, "claude/sessions/101.json")
				record := claudeRegistry(101, "main", claudeStart)
				switch field {
				case "deleted":
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				case "corrupt":
					if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
						t.Fatal(err)
					}
				case "pid":
					record[field] = 102
					writeRecords(t, path, record)
				default:
					record[field] = "changed"
					writeRecords(t, path, record)
				}
				return run(name, args...)
			}
			if _, err := r.Locate([]int{101}); err == nil || !strings.Contains(err.Error(), "changed") {
				t.Fatal(err)
			}
		})
	}
}

func TestClaudeRegistryProcessRecheck(t *testing.T) {
	for _, failure := range []bool{false, true} {
		base := t.TempDir()
		registeredClaude(t, base, 101, "main")
		r := registryResolver(t, base)
		calls := 0
		r.Run = func(string, ...string) ([]byte, error) {
			calls++
			if calls == 1 {
				return []byte(claudeStart), nil
			}
			if failure {
				return nil, errors.New("process disappeared")
			}
			return []byte("Sun Sep 13 03:00:00 2026"), nil
		}
		if _, err := r.Locate([]int{101}); err == nil || !strings.Contains(err.Error(), "process changed") {
			t.Fatal(err)
		}
	}
}

func TestClaudeRegistryMissingTranscriptIsActionable(t *testing.T) {
	base := t.TempDir()
	path := registeredClaude(t, base, 101, "main")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := registryResolver(t, base).Locate([]int{100, 101}); err == nil || !strings.Contains(err.Error(), "finish a conversation turn") {
		t.Fatal(err)
	}
}

func TestClaudeRegistryRootFilesOnly(t *testing.T) {
	base := t.TempDir()
	path := registeredClaude(t, base, 101, "main")
	if err := os.Rename(path, filepath.Join(filepath.Dir(path), "agent-main.jsonl")); err != nil {
		t.Fatal(err)
	}
	writeRecords(t, filepath.Join(base, "claude/projects/workspace/main/subagents/main.jsonl"), claudeFixtureRecord("user", "sub", "", claudeUserMessage("Subagent")))
	if _, err := registryResolver(t, base).Locate([]int{101}); err == nil || !strings.Contains(err.Error(), "no transcript file") {
		t.Fatal(err)
	}
}

func TestClaudeRegistryRejectsEscapingSymlink(t *testing.T) {
	base := t.TempDir()
	path := registeredClaude(t, base, 101, "main")
	outside := writeRecords(t, filepath.Join(t.TempDir(), "main.jsonl"), map[string]any{"type": "user", "sessionId": "main", "message": claudeUserMessage("Outside")})
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err := registryResolver(t, base).Locate([]int{101}); err == nil || !strings.Contains(err.Error(), "canonical root") {
		t.Fatal(err)
	}
}

func TestClaudeRegistryRejectsCorruptionAndUnsafeIdentity(t *testing.T) {
	for _, field := range []string{"pid", "sessionId", "procStart", "corrupt"} {
		t.Run(field, func(t *testing.T) {
			base := t.TempDir()
			registeredClaude(t, base, 101, "main")
			path := filepath.Join(base, "claude/sessions/101.json")
			record := claudeRegistry(101, "main", claudeStart)
			switch field {
			case "pid":
				record[field] = 102
			case "sessionId":
				record[field] = "../main"
			case "procStart":
				record[field] = ""
			case "corrupt":
				if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if field != "corrupt" {
				writeRecords(t, path, record)
			}
			if _, err := registryResolver(t, base).Locate([]int{101}); err == nil {
				t.Fatal("unsafe registry accepted")
			}
		})
	}
}

func TestClaudeRegistryRequiresInteractiveCLIAndHostPIDDomain(t *testing.T) {
	for _, field := range []string{"kind", "entrypoint", "pidDomain"} {
		t.Run(field, func(t *testing.T) {
			base := t.TempDir()
			registeredClaude(t, base, 101, "main")
			record := claudeRegistry(101, "main", claudeStart)
			record[field] = "other"
			writeRecords(t, filepath.Join(base, "claude/sessions/101.json"), record)
			if _, err := registryResolver(t, base).Locate([]int{101}); err == nil {
				t.Fatal("nonforeground registry selected")
			}
		})
	}
}

func TestClaudeRegistrySelectsCanonicalNotRawAlias(t *testing.T) {
	base := t.TempDir()
	path := registeredClaude(t, base, 101, "new-session")
	record := map[string]any{"type": "user", "sessionId": "new-session", "session_id": "old-session", "uuid": "u", "message": claudeUserMessage("New canonical conversation")}
	writeRecords(t, path, record)
	writeRecords(t, filepath.Join(base, "claude/projects/workspace/old-session.jsonl"), map[string]any{"type": "user", "sessionId": "old-session", "message": claudeUserMessage("Old alias conversation")})
	got, err := registryResolver(t, base).Locate([]int{101})
	if err != nil || got.Path != path {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestClaudeRegistryRejectsMixedTranscriptIDsAndCorruptLines(t *testing.T) {
	base := t.TempDir()
	path := registeredClaude(t, base, 101, "main")
	appendRaw(t, path, "{\"type\":\"user\",\"sessionId\":\"other\",\"message\":{\"role\":\"user\",\"content\":\"Other\"}}\n")
	if _, err := registryResolver(t, base).Locate([]int{101}); err == nil || !strings.Contains(err.Error(), "multiple sessionId") {
		t.Fatal(err)
	}
	registeredClaude(t, base, 101, "main")
	appendRaw(t, path, "corrupt complete line\n")
	if _, err := registryResolver(t, base).Locate([]int{101}); err == nil || !strings.Contains(err.Error(), "invalid JSONL") {
		t.Fatal(err)
	}
}

func TestClaudeRegistryMultipleOwnedSessionsFailClosed(t *testing.T) {
	base := t.TempDir()
	registeredClaude(t, base, 101, "one")
	registeredClaude(t, base, 102, "two")
	if _, err := registryResolver(t, base).Locate([]int{101, 102}); err == nil || !strings.Contains(err.Error(), "found 2 sessions") {
		t.Fatal(err)
	}
}

func TestClaudeRegistryInvalidPIDsAndInspectionError(t *testing.T) {
	for _, pids := range [][]int{nil, {0}, {-1}, {101, -1}} {
		if _, err := (Resolver{}).Locate(pids); err == nil {
			t.Fatal("invalid owner PIDs accepted")
		}
	}
	base := t.TempDir()
	registeredClaude(t, base, 101, "main")
	r := registryResolver(t, base)
	r.Run = func(string, ...string) ([]byte, error) { return nil, errors.New("inspection denied") }
	if _, err := r.Locate([]int{101}); err == nil || !strings.Contains(err.Error(), "inspection denied") {
		t.Fatal(err)
	}
}

func TestClaudeConfigDirOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "~/custom-claude")
	if got := ConfigDir(); got != filepath.Join(home, "custom-claude") {
		t.Fatal(got)
	}
}

func TestClaudeRegistryKeepsValidatedCanonicalPathWhenAliasChanges(t *testing.T) {
	base := t.TempDir()
	registeredClaude(t, base, 101, "main")
	projects := filepath.Join(base, "claude", "projects")
	original := filepath.Join(base, "original-projects")
	if err := os.Rename(projects, original); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(original, projects); err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(base, "replacement-projects")
	writeRecords(t, filepath.Join(replacement, "workspace", "main.jsonl"), map[string]any{
		"type": "user", "sessionId": "main", "uuid": "outside", "parentUuid": nil,
		"message": map[string]any{"role": "user", "content": "UNOWNED replacement"},
	})
	r := registryResolver(t, base)
	run := r.Run
	calls := 0
	r.Run = func(name string, args ...string) ([]byte, error) {
		calls++
		if calls == 2 {
			if err := os.Remove(projects); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(replacement, projects); err != nil {
				t.Fatal(err)
			}
		}
		return run(name, args...)
	}
	source, err := r.Locate([]int{101})
	if err != nil {
		t.Fatal(err)
	}
	expected, err := filepath.EvalSymlinks(filepath.Join(original, "workspace", "main.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if source.Path != expected {
		t.Fatalf("mutable alias replaced validated path: %s != %s", source.Path, expected)
	}
	doc, err := Render(source.Path)
	if err != nil || !strings.Contains(doc.Markdown, "User question") || strings.Contains(doc.Markdown, "UNOWNED") {
		t.Fatalf("unvalidated replacement rendered: %v", err)
	}
}
