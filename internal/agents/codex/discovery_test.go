package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexSelectsOnlyUniqueOwnedForkChain(t *testing.T) {
	for _, tc := range []struct {
		name         string
		ids, parents []string
		want         string
	}{
		{"parent and fork", []string{"a", "b"}, []string{"", "a"}, "b"},
		{"reverse order", []string{"b", "a"}, []string{"a", ""}, "b"},
		{"multi-hop", []string{"b", "c", "a"}, []string{"a", "b", ""}, "c"},
		{"external ancestor", []string{"a", "b"}, []string{"not-open", "a"}, "b"},
		{"single fork parent not open", []string{"b"}, []string{"not-open"}, "b"},
		{"siblings", []string{"a", "b", "c"}, []string{"", "a", "a"}, ""},
		{"unrelated", []string{"a", "b"}, []string{"", ""}, ""},
		{"fork plus unrelated", []string{"a", "b", "c"}, []string{"", "a", ""}, ""},
		{"cycle", []string{"a", "b"}, []string{"b", "a"}, ""},
		{"disconnected cycle and tip", []string{"a", "b", "c"}, []string{"b", "a", ""}, ""},
		{"self-reference", []string{"a"}, []string{"a"}, ""},
		{"duplicate IDs", []string{"a", "a"}, []string{"", ""}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			paths := []string{}
			wanted := ""
			for i, id := range tc.ids {
				meta := map[string]any{"id": id, "source": "cli"}
				if tc.parents[i] != "" {
					meta["forked_from_id"] = tc.parents[i]
				}
				path := writeRecords(t, filepath.Join(dir, "rollout-"+string(rune('a'+i))+".jsonl"), map[string]any{"type": "session_meta", "payload": meta})
				paths = append(paths, path)
				if id == tc.want {
					wanted = path
				}
			}
			got, err := resolver(t, paths...).Locate([]int{100, 101})
			if tc.want == "" {
				if err == nil {
					t.Fatalf("ambiguous/invalid selection accepted: %+v", got)
				}
				return
			}
			if err != nil || got.Path != wanted {
				t.Fatalf("got %+v %v, want %s", got, err, wanted)
			}
		})
	}
}

func TestCodexForkIgnoresWorkersAndHistoryBaseForSelection(t *testing.T) {
	dir := t.TempDir()
	parent := writeRecords(t, filepath.Join(dir, "rollout-parent.jsonl"), map[string]any{"type": "session_meta", "payload": map[string]any{"id": "parent", "source": "cli"}})
	fork := writeRecords(t, filepath.Join(dir, "rollout-fork.jsonl"), map[string]any{"type": "session_meta", "payload": map[string]any{"id": "fork", "source": "cli", "forked_from_id": "parent", "history_base": map[string]any{"thread_id": "unrelated", "end_ordinal_exclusive": 999}}})
	worker := writeRecords(t, filepath.Join(dir, "rollout-worker.jsonl"), map[string]any{"type": "session_meta", "payload": map[string]any{"id": "worker", "source": map[string]any{"subagent": map[string]any{}}, "forked_from_id": "fork"}})
	got, err := resolver(t, parent, worker, fork).Locate([]int{100, 101})
	if err != nil || got.Path != fork {
		t.Fatalf("wrong fork: %+v %v", got, err)
	}
	_, err = Render(got.Path)
	if err == nil || !strings.Contains(err.Error(), "No saved public messages") {
		t.Fatalf("empty fork should show notice, not parent content: %v", err)
	}
}

func TestCodexHistoryReferenceDoesNotDisambiguateUnrelatedThreads(t *testing.T) {
	dir := t.TempDir()
	a := writeRecords(t, filepath.Join(dir, "rollout-a.jsonl"), map[string]any{"type": "session_meta", "payload": map[string]any{"id": "a", "source": "cli"}})
	b := writeRecords(t, filepath.Join(dir, "rollout-b.jsonl"), map[string]any{"type": "session_meta", "payload": map[string]any{"id": "b", "source": "cli", "parent_thread_id": "a", "history_base": map[string]any{"thread_id": "a"}}})
	if _, err := resolver(t, a, b).Locate([]int{100, 101}); err == nil {
		t.Fatal("non-fork metadata selected a thread")
	}
}

func TestCodexDiscoveryDeduplicatesPathsBeforeForkReduction(t *testing.T) {
	dir := t.TempDir()
	a := writeRecords(t, filepath.Join(dir, "rollout-a.jsonl"), map[string]any{"type": "session_meta", "payload": map[string]any{"id": "a", "source": "cli"}})
	alias := filepath.Join(dir, "rollout-alias.jsonl")
	if err := os.Symlink(a, alias); err != nil {
		t.Fatal(err)
	}
	b := writeRecords(t, filepath.Join(dir, "rollout-b.jsonl"), map[string]any{"type": "session_meta", "payload": map[string]any{"id": "b", "source": "cli", "forked_from_id": "a"}})
	got, err := resolver(t, a, alias, b, b).Locate([]int{100, 101})
	if err != nil || got.Path != b {
		t.Fatalf("alias broke selection: %+v %v", got, err)
	}
}

func TestCodexForkHeaderValidationAndHeaderOnlyDiscovery(t *testing.T) {
	for _, bad := range []any{7, "", true, map[string]any{}} {
		dir := t.TempDir()
		file := writeRecords(t, filepath.Join(dir, "rollout-main.jsonl"), map[string]any{"type": "session_meta", "payload": map[string]any{"id": "main", "source": "cli", "forked_from_id": bad}})
		if _, err := resolver(t, file).Locate([]int{100, 101}); err == nil {
			t.Fatalf("invalid parent accepted: %v", bad)
		}
	}
	dir := t.TempDir()
	parent := writeRecords(t, filepath.Join(dir, "rollout-parent.jsonl"), map[string]any{"type": "session_meta", "payload": map[string]any{"id": "parent", "source": "cli"}})
	f, err := os.OpenFile(parent, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString("invalid retained parent body\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	fork := writeRecords(t, filepath.Join(dir, "rollout-fork.jsonl"), map[string]any{"type": "session_meta", "payload": map[string]any{"id": "fork", "source": "cli", "forked_from_id": "parent"}})
	got, err := resolver(t, parent, fork).Locate([]int{100, 101})
	if err != nil || got.Path != fork {
		t.Fatalf("read unselected parent's body: %+v %v", got, err)
	}
}

func TestCodexMissingOwnedRolloutGivesLaunchGuidance(t *testing.T) {
	dir := t.TempDir()
	// A valid unrelated transcript on disk must not become an implicit fallback.
	writeRecords(t, filepath.Join(dir, "rollout-other.jsonl"), map[string]any{"type": "session_meta", "payload": map[string]any{"id": "other", "source": "cli"}})
	_, err := resolver(t).Locate([]int{100, 101})
	if err == nil || !strings.Contains(err.Error(), "No local Codex transcript found") || !strings.Contains(err.Error(), "codex --no-daemon") || !strings.Contains(err.Error(), "finish a conversation turn") {
		t.Fatalf("missing mode-specific guidance: %v", err)
	}
}

func TestCodexInteractiveSessionOrigins(t *testing.T) {
	for _, source := range []string{"cli", "vscode"} {
		t.Run(source, func(t *testing.T) {
			dir := t.TempDir()
			main := writeRecords(t, filepath.Join(dir, "rollout-main.jsonl"), map[string]any{"type": "session_meta", "payload": map[string]any{"id": "main", "source": source}})
			files := []string{main}
			for i, excluded := range []any{
				"exec", "mcp", "unknown", "other", nil,
				map[string]any{"custom": "example"},
				map[string]any{"internal": "guardian"},
				map[string]any{"subagent": map[string]any{"thread_spawn": map[string]any{"parent_thread_id": "main", "depth": 1}}},
			} {
				id := fmt.Sprintf("excluded-%d", i)
				files = append(files, writeRecords(t, filepath.Join(dir, "rollout-"+id+".jsonl"), map[string]any{"type": "session_meta", "payload": map[string]any{"id": id, "source": excluded}}))
			}
			got, err := resolver(t, files...).Locate([]int{100, 101})
			if err != nil || got.Path != main {
				t.Fatalf("interactive origin %s not selected: %+v %v", source, got, err)
			}
			if _, err := resolver(t, files[1:]...).Locate([]int{100, 101}); err == nil {
				t.Fatal("noninteractive origin selected without an interactive candidate")
			}
		})
	}
}

func TestCodexForkAcrossInteractiveOrigins(t *testing.T) {
	dir := t.TempDir()
	parent := writeRecords(t, filepath.Join(dir, "rollout-parent.jsonl"), map[string]any{"type": "session_meta", "payload": map[string]any{"id": "parent", "source": "vscode"}})
	fork := writeRecords(t, filepath.Join(dir, "rollout-fork.jsonl"), map[string]any{"type": "session_meta", "payload": map[string]any{"id": "fork", "source": "cli", "forked_from_id": "parent"}})
	got, err := resolver(t, parent, fork).Locate([]int{100, 101})
	if err != nil || got.Path != fork {
		t.Fatalf("cross-origin fork selection failed: %+v %v", got, err)
	}
	unrelated := writeRecords(t, filepath.Join(dir, "rollout-other.jsonl"), map[string]any{"type": "session_meta", "payload": map[string]any{"id": "other", "source": "vscode"}})
	if _, err := resolver(t, parent, fork, unrelated).Locate([]int{100, 101}); err == nil {
		t.Fatal("unrelated app-server origin bypassed ambiguity check")
	}
}

func TestCodexRejectsMissingOrInvalidOwnerPIDsBeforeInspection(t *testing.T) {
	for _, tc := range []struct {
		name string
		pids []int
		want string
	}{
		{"missing", nil, "no owner PID for codex"},
		{"empty", []int{}, "no owner PID for codex"},
		{"zero", []int{0}, "invalid owner PID for codex"},
		{"negative", []int{-1}, "invalid owner PID for codex"},
		{"mixed", []int{100, 0}, "invalid owner PID for codex"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Resolver{Run: func(name string, args ...string) ([]byte, error) {
				t.Fatalf("inspected process before validating owners: %s %v", name, args)
				return nil, nil
			}}
			if _, err := r.Locate(tc.pids); err == nil || err.Error() != tc.want {
				t.Fatalf("got %v, want %s", err, tc.want)
			}
		})
	}
}

func TestCodexLocatePreservesInspectionErrors(t *testing.T) {
	want := fmt.Errorf("lsof unavailable")
	r := Resolver{Run: func(name string, args ...string) ([]byte, error) {
		return nil, want
	}}
	if _, err := r.Locate([]int{100}); err != want {
		t.Fatalf("got %v, want original inspection error %v", err, want)
	}
}

func TestDetectRecognizesOnlyCodexHeader(t *testing.T) {
	for _, tc := range []struct {
		name    string
		records []map[string]any
		id      string
		ok      bool
	}{
		{"empty", nil, "", false},
		{"different format", []map[string]any{{"type": "session", "id": "main"}}, "", false},
		{"header", []map[string]any{codexMeta("main")}, "main", true},
		{"missing ID", []map[string]any{{"type": "session_meta"}}, "", true},
		{"later header", []map[string]any{{"type": "event_msg"}, codexMeta("main")}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, ok := Detect(tc.records)
			if id != tc.id || ok != tc.ok {
				t.Fatalf("got (%q, %v), want (%q, %v)", id, ok, tc.id, tc.ok)
			}
		})
	}
}
