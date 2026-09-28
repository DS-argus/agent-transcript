package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
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
	defer file.Close()
	for _, record := range records {
		if err := json.NewEncoder(file).Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func resolver(t *testing.T, base string, files ...string) Resolver {
	t.Helper()
	return Resolver{
		GJCDir: filepath.Join(base, "gjc"), ClaudeDir: filepath.Join(base, "claude"),
		Identity: func(pid int) (string, error) { return "darwin:10:20", nil },
		Run: func(name string, args ...string) ([]byte, error) {
			switch name {
			case "ps":
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
		},
	}
}

func TestCodexWorkerExclusion(t *testing.T) {
	dir := t.TempDir()
	main := writeRecords(t, filepath.Join(dir, "rollout-main.jsonl"), map[string]any{"type": "session_meta", "payload": map[string]any{"id": "main", "source": "cli"}})
	worker := writeRecords(t, filepath.Join(dir, "rollout-worker.jsonl"), map[string]any{"type": "session_meta", "payload": map[string]any{"id": "worker", "source": map[string]any{"subagent": map[string]any{}}}})
	got, err := resolver(t, dir, worker, main, main).Locate("codex", []int{100, 101})
	if err != nil || got != (Source{"codex", main}) {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestClaudeRootFilesOnly(t *testing.T) {
	enableClaudeAdapter(t)
	dir := t.TempDir()
	record := map[string]any{"type": "user", "sessionId": "main", "uuid": "u", "message": map[string]any{"role": "user", "content": "question"}}
	main := writeRecords(t, filepath.Join(dir, "claude/projects/workspace/main.jsonl"), record)
	worker := writeRecords(t, filepath.Join(dir, "claude/projects/workspace/main/subagents/worker.jsonl"), record)
	unrelated := writeRecords(t, filepath.Join(dir, "other/main.jsonl"), record)
	got, err := resolver(t, dir, worker, main, unrelated).Locate("claude", []int{100, 101})
	if err != nil || got != (Source{"claude", main}) {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestAmbiguityNeverPicksNewest(t *testing.T) {
	enableClaudeAdapter(t)
	dir := t.TempDir()
	first := writeRecords(t, filepath.Join(dir, "rollout-main.jsonl"), map[string]any{"type": "session_meta", "payload": map[string]any{"id": "main", "source": "cli"}})
	second := writeRecords(t, filepath.Join(dir, "rollout-other.jsonl"), map[string]any{"type": "session_meta", "payload": map[string]any{"id": "other", "source": "cli"}})
	for _, files := range [][]string{{first, second}, {second, first}} {
		_, err := resolver(t, dir, files...).Locate("codex", []int{100, 101})
		if err == nil || !strings.Contains(err.Error(), "expected exactly one") {
			t.Fatalf("must reject missing or ambiguous sessions: %v", err)
		}
	}
	claude := writeRecords(t, filepath.Join(dir, "claude/projects/project/main.jsonl"), map[string]any{"type": "user", "sessionId": "main", "message": map[string]any{"role": "user", "content": "question"}})
	got, err := resolver(t, dir, first, second, claude).Locate("claude", []int{100, 101})
	if err != nil || got.Harness != "claude" {
		t.Fatalf("selected harness: %+v %v", got, err)
	}
}
