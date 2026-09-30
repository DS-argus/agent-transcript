package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"agent-transcript/internal/agents"
)

func writeRecords(t *testing.T, path string, records ...map[string]any) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if err := json.NewEncoder(file).Encode(record); err != nil {
			file.Close()
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

const fixtureStart = "Mon Jan  1 00:00:00 2024"

func resolver(t *testing.T, base string, files ...string) Resolver {
	t.Helper()
	return Resolver{GJCDir: filepath.Join(base, "gjc"), ClaudeDir: filepath.Join(base, "claude"),
		Identity: func(pid int) (string, error) { return "darwin:10:20", nil },
		Run: func(name string, args ...string) ([]byte, error) {
			switch name {
			case "ps":
				if len(args) == 4 && args[0] == "-p" && args[3] == "lstart=" {
					return []byte(fixtureStart), nil
				}
				return []byte("100 1\n101 100\n200 1\n"), nil
			case "lsof":
				if !reflect.DeepEqual(args, []string{"-a", "-p", "100,101", "-Fn"}) {
					t.Fatalf("wrong lsof targets: %v", args)
				}
				var lines []string
				for _, path := range files {
					lines = append(lines, "n"+path)
				}
				return []byte(strings.Join(lines, "\n")), nil
			default:
				return nil, fmt.Errorf("unexpected command %s", name)
			}
		}}
}
func registerClaude(t *testing.T, base string, pid int, sid string) {
	t.Helper()
	writeRecords(t, filepath.Join(base, "claude", "sessions", fmt.Sprint(pid)+".json"), map[string]any{"pid": pid, "sessionId": sid, "procStart": fixtureStart, "pidDomain": runtime.GOOS, "kind": "interactive", "entrypoint": "cli"})
}
func TestCodexWorkerExclusion(t *testing.T) {
	dir := t.TempDir()
	main := writeRecords(t, filepath.Join(dir, "rollout-main.jsonl"), map[string]any{"type": "session_meta", "payload": map[string]any{"id": "main", "source": "cli"}})
	worker := writeRecords(t, filepath.Join(dir, "rollout-worker.jsonl"), map[string]any{"type": "session_meta", "payload": map[string]any{"id": "worker", "source": map[string]any{"subagent": map[string]any{}}}})
	got, err := resolver(t, dir, worker, main, main).Locate("codex", []int{100, 101})
	if err != nil || got != (agents.Source{Harness: "codex", Path: main}) {
		t.Fatalf("%+v %v", got, err)
	}
}
func TestClaudeRootRegistryFilesOnly(t *testing.T) {
	dir := t.TempDir()
	record := map[string]any{"type": "user", "sessionId": "main", "uuid": "u", "message": map[string]any{"role": "user", "content": "question"}}
	main := writeRecords(t, filepath.Join(dir, "claude/projects/workspace/main.jsonl"), record)
	writeRecords(t, filepath.Join(dir, "claude/projects/workspace/main/subagents/worker.jsonl"), record)
	writeRecords(t, filepath.Join(dir, "other/main.jsonl"), record)
	registerClaude(t, dir, 100, "main")
	main, err := filepath.EvalSymlinks(main)
	if err != nil {
		t.Fatal(err)
	}
	got, err := resolver(t, dir).Locate("claude", []int{100, 101})
	if err != nil || got != (agents.Source{Harness: "claude", Path: main}) {
		t.Fatalf("%+v %v", got, err)
	}
}
func TestClaudeMissingRegistryNeverUsesOwnedFiles(t *testing.T) {
	dir := t.TempDir()
	path := writeRecords(t, filepath.Join(dir, "claude/projects/workspace/main.jsonl"), map[string]any{"type": "user", "sessionId": "main", "uuid": "u", "message": map[string]any{"role": "user", "content": "question"}})
	if source, err := resolver(t, dir, path).Locate("claude", []int{100, 101}); err == nil {
		t.Fatalf("unregistered file adopted: %+v", source)
	}
}
func TestAmbiguityNeverPicksNewest(t *testing.T) {
	dir := t.TempDir()
	first := writeRecords(t, filepath.Join(dir, "rollout-main.jsonl"), map[string]any{"type": "session_meta", "payload": map[string]any{"id": "main", "source": "cli"}})
	second := writeRecords(t, filepath.Join(dir, "rollout-other.jsonl"), map[string]any{"type": "session_meta", "payload": map[string]any{"id": "other", "source": "cli"}})
	for _, files := range [][]string{{first, second}, {second, first}} {
		if _, err := resolver(t, dir, files...).Locate("codex", []int{100, 101}); err == nil || !strings.Contains(err.Error(), "expected exactly one") {
			t.Fatalf("ambiguous sessions: %v", err)
		}
	}
	registerClaude(t, dir, 100, "main")
	writeRecords(t, filepath.Join(dir, "claude/projects/project/main.jsonl"), map[string]any{"type": "user", "sessionId": "main", "uuid": "u", "message": map[string]any{"role": "user", "content": "question"}})
	got, err := resolver(t, dir, first, second).Locate("claude", []int{100, 101})
	if err != nil || got.Harness != "claude" {
		t.Fatalf("selected harness: %+v %v", got, err)
	}
}
