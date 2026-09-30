package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"agent-transcript/internal/process"
)

func claudeMessage(session, uuid, text string) map[string]any {
	return map[string]any{
		"type": "assistant", "sessionId": session, "uuid": uuid, "parentUuid": nil,
		"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}},
	}
}

func writeClaudeRegistration(t *testing.T, s *server, session string) string {
	t.Helper()
	pid, err := strconv.Atoi(s.tmux("display-message", "-p", "-t", s.source, "#{pane_pid}"))
	if err != nil {
		t.Fatal(err)
	}
	start, err := process.Run("ps", "-p", strconv.Itoa(pid), "-o", "lstart=")
	if err != nil {
		t.Fatal(err)
	}
	return writeJSON(t, filepath.Join(s.dir, ".claude", "sessions", strconv.Itoa(pid)+".json"), map[string]any{
		"pid": pid, "sessionId": session, "procStart": strings.TrimSpace(string(start)),
		"pidDomain": runtime.GOOS, "kind": "interactive", "entrypoint": "cli",
	})
}

func TestClaudeOwnedRegistryWithoutOpenTranscript(t *testing.T) {
	t.Setenv("AGENT_TRANSCRIPT_OPEN_FILES", "")
	s, path := autoFixtureServer(t, "claude", claudeMessage("main", "a", "Closed descriptor answer"))
	pid := s.tmux("display-message", "-p", "-t", s.source, "#{pane_pid}")
	files, err := process.Run("lsof", "-a", "-p", pid, "-Fn")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(files), path) {
		t.Fatal("fixture unexpectedly owns an open transcript descriptor")
	}
	pane := s.open()
	if !strings.Contains(s.capture(pane), "Closed descriptor answer") {
		t.Fatal("registry-owned transcript with closed descriptor not displayed")
	}
	s.close(pane)
}

func TestClaudeRegistrySelectsSourceNotUnrelatedOrSubagent(t *testing.T) {
	unrelated := writeJSON(t, filepath.Join(t.TempDir(), "unrelated.jsonl"), claudeMessage("unrelated", "other", "Unrelated answer"))
	t.Setenv("AGENT_TRANSCRIPT_OPEN_FILES", unrelated)
	s, _ := autoFixtureServer(t, "claude", claudeMessage("main", "a", "Owned source answer"))
	writeJSON(t, filepath.Join(s.dir, ".claude", "projects", "project", "unrelated.jsonl"), claudeMessage("unrelated", "other", "Unrelated answer"))
	writeJSON(t, filepath.Join(s.dir, ".claude", "projects", "project", "main", "subagents", "agent-worker.jsonl"), claudeMessage("main", "worker", "Subagent answer"))
	pane := s.open()
	screen := s.capture(pane)
	if !strings.Contains(screen, "Owned source answer") || strings.Contains(screen, "Unrelated answer") || strings.Contains(screen, "Subagent answer") {
		t.Fatal("wrong Claude source:", screen)
	}
	s.close(pane)
}

func TestClaudeSameProcessRegistrySessionSwitch(t *testing.T) {
	s, original := autoFixtureServer(t, "claude", claudeMessage("main", "a", "Original session answer"))
	oldViewer := s.open("--position", "right", "--size", "35%")
	pid := s.tmux("display-message", "-p", "-t", s.source, "#{pane_pid}")
	oldSignature := s.tmux("show-options", "-p", "-v", "-t", oldViewer, viewerSignatureOption)
	newPath := writeJSON(t, filepath.Join(s.dir, ".claude", "projects", "project", "next.jsonl"), claudeMessage("next", "b", "Switched session answer"))
	writeClaudeRegistration(t, s, "next")
	if pid != s.tmux("display-message", "-p", "-t", s.source, "#{pane_pid}") {
		t.Fatal("session switch changed the source process")
	}
	_, stderr, err := s.launch(oldViewer)
	if err == nil || !strings.Contains(stderr, "source session changed; open from the agent pane") {
		t.Fatalf("stale viewer acted for the new session: %q %v", stderr, err)
	}
	if len(s.panes()) != 2 || s.tmux("show-options", "-p", "-v", "-t", oldViewer, viewerSignatureOption) != oldSignature || !strings.Contains(s.capture(oldViewer), "Original session answer") {
		t.Fatal("stale viewer was adopted or changed")
	}
	stdout, stderr, err := s.launch(s.source, "--position", "right", "--size", "35%")
	if err != nil || stdout != "" || stderr != "" {
		t.Fatalf("source session refresh: %q %q %v", stdout, stderr, err)
	}
	var newViewer string
	for _, pane := range s.panes() {
		if pane != s.source && pane != oldViewer {
			newViewer = pane
		}
	}
	if newViewer == "" || len(s.panes()) != 3 {
		t.Fatalf("missing new session viewer: %v", s.panes())
	}
	s.wait(func() bool { return strings.Contains(s.capture(newViewer), "Switched session answer") })
	screen := s.capture(newViewer)
	if !strings.Contains(screen, "Switched session answer") || strings.Contains(screen, "Original session answer") {
		t.Fatal("new viewer did not use switched session:", screen)
	}
	s.tmux("send-keys", "-t", oldViewer, "Escape", "q")
	s.wait(func() bool { return len(s.panes()) == 2 })
	s.close(newViewer)
	for _, path := range []string{original, newPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("original transcript removed:", err)
		}
	}
}

func TestClaudeRendererCanonicalSessionMismatchCannotPublishViewer(t *testing.T) {
	root := isolatedCache(t)
	s, path := autoFixtureServer(t, "claude", claudeMessage("main", "a", "Original answer"))
	identity := snapshotIdentity{path: path, harness: "claude", session: "main", source: s.source,
		sourcePID:    s.tmux("display-message", "-p", "-t", s.source, "#{pane_pid}"),
		sourceWindow: s.tmux("display-message", "-p", "-t", s.source, "#{window_id}")}
	record := claudeMessage("different", "b", "Wrong session answer")
	record["session_id"] = "main" // Wire identity must not override canonical outer sessionId.
	writeJSON(t, path, record)
	if _, err := writeSnapshot(identity); err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("renderer published changed canonical identity: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "snapshots"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed renderer leaked snapshot: %v %v", entries, err)
	}
	_, stderr, err := s.launch()
	if err == nil || !strings.Contains(stderr, "disagree") {
		t.Fatalf("canonical mismatch accepted: %q %v", stderr, err)
	}
	if len(s.panes()) != 1 {
		t.Fatal("canonical mismatch published a viewer")
	}
	s.clean()
}

func TestClaudeRegistryFailuresKeepConcreteDiagnostic(t *testing.T) {
	for _, failure := range []string{"corrupt", "missing", "stale", "missing transcript"} {
		t.Run(failure, func(t *testing.T) {
			s, path := autoFixtureServer(t, "claude", claudeMessage("main", "a", "Saved answer"))
			registry := writeClaudeRegistration(t, s, "main")
			want := "identified claude foreground TUI, but found 0 sessions"
			switch failure {
			case "corrupt":
				if err := os.WriteFile(registry, []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
				want = "invalid Claude session registry"
			case "missing":
				if err := os.Remove(registry); err != nil {
					t.Fatal(err)
				}
			case "stale":
				pid, err := strconv.Atoi(s.tmux("display-message", "-p", "-t", s.source, "#{pane_pid}"))
				if err != nil {
					t.Fatal(err)
				}
				writeJSON(t, registry, map[string]any{"pid": pid, "sessionId": "main", "procStart": "Thu Jan 1 00:00:00 1970", "pidDomain": runtime.GOOS, "kind": "interactive", "entrypoint": "cli"})
			case "missing transcript":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				want = "finish a conversation turn"
			}
			_, stderr, err := s.launch()
			if err == nil || !strings.Contains(stderr, want) || strings.Contains(stderr, "Run this from a supported agent pane.") {
				t.Fatalf("wrong Claude discovery diagnostic: %q %v", stderr, err)
			}
			if len(s.panes()) != 1 {
				t.Fatal("registry failure published a viewer")
			}
			s.clean()
		})
	}
}
