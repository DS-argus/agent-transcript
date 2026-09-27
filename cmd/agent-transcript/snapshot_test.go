package main

import "testing"

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
