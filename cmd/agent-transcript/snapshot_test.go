package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotSignatureIncludesSessionAndSourceIdentity(t *testing.T) {
	base := snapshotIdentity{
		path: "/tmp/session.jsonl", harness: "claude", session: "session-a",
		source: "%1", sourcePID: "101", sourceWindow: "@1",
	}
	if base.signature() == (snapshotIdentity{path: "/tmp/session.jsonl", harness: "claude", session: "session-a", source: "%2", sourcePID: "101", sourceWindow: "@1"}).signature() {
		t.Fatal("source pane identity was omitted from signature")
	}
	if base.signature() == (snapshotIdentity{path: "/tmp/session.jsonl", harness: "claude", session: "session-b", source: "%1", sourcePID: "101", sourceWindow: "@1"}).signature() {
		t.Fatal("session identity was omitted from signature")
	}
	if base.signature() == (snapshotIdentity{path: "/tmp/other.jsonl", harness: "claude", session: "session-a", source: "%1", sourcePID: "101", sourceWindow: "@1"}).signature() {
		t.Fatal("transcript path was omitted from signature")
	}
}

func TestSnapshotRejectsChangedTranscriptIdentity(t *testing.T) {
	root := isolatedCache(t)
	path := writeJSON(t, filepath.Join(t.TempDir(), "claude.jsonl"), map[string]any{
		"type": "user", "uuid": "u", "parentUuid": nil, "sessionId": "new-session",
		"message": map[string]any{"role": "user", "content": "new conversation"},
	})
	if _, err := writeSnapshot(snapshotIdentity{path: path, harness: "claude", session: "old-session"}); err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("stale snapshot accepted: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "snapshots"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed snapshot leaked: %v %v", entries, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("source transcript changed", err)
	}
}
