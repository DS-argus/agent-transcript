package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexForkViewerWhileParentRolloutRemainsOpen(t *testing.T) {
	dir := t.TempDir()
	parent := writeJSON(t, filepath.Join(dir, "rollout-parent.jsonl"),
		map[string]any{"type": "session_meta", "payload": map[string]any{"id": "parent", "source": "cli"}},
		map[string]any{"type": "event_msg", "payload": map[string]any{"type": "item_completed", "thread_id": "parent", "turn_id": "p", "item": map[string]any{"type": "AgentMessage", "id": "a", "content": []any{map[string]any{"type": "Text", "text": "PARENT-ONLY-CONTENT"}}}}})
	meta := map[string]any{"type": "session_meta", "payload": map[string]any{"id": "fork", "source": "cli", "forked_from_id": "parent"}}
	fork := writeJSON(t, filepath.Join(dir, "rollout-fork.jsonl"), meta)
	t.Setenv("AGENT_TRANSCRIPT_OPEN_FILES", strings.Join([]string{parent, fork}, string(os.PathListSeparator)))
	s := newServer(t, "codex")
	_, stderr, err := s.launch()
	if err == nil || !strings.Contains(stderr, "No saved public messages") {
		t.Fatalf("empty fork selected parent or remained ambiguous: %s %v", stderr, err)
	}
	if len(s.panes()) != 1 {
		t.Fatal("empty fork created viewer")
	}
	writeJSON(t, fork, meta, map[string]any{"type": "event_msg", "payload": map[string]any{"type": "item_completed", "thread_id": "fork", "turn_id": "f", "item": map[string]any{"type": "AgentMessage", "id": "a", "content": []any{map[string]any{"type": "Text", "text": "FORK-ONLY-CONTENT"}}}}})
	viewer := s.open()
	screen := s.capture(viewer)
	if !strings.Contains(screen, "FORK-ONLY-CONTENT") || strings.Contains(screen, "PARENT-ONLY-CONTENT") {
		t.Fatalf("wrong fork content: %s", screen)
	}
	s.close(viewer)
}
