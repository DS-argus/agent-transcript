package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readerDocument(t *testing.T, lines int) string {
	var text strings.Builder
	text.WriteString("# Reader heading\n\n**Bold text** and `inline code`.\n\n")
	for i := 0; i < lines; i++ {
		text.WriteString("Unicode: 한글 snapshot.\n\n")
		text.WriteString("Paragraph to scroll through.\n\n")
	}
	text.WriteString("END-OF-TRANSCRIPT\n")
	path := writeJSON(t, filepath.Join(t.TempDir(), "rollout-main.jsonl"),
		map[string]any{"type": "session_meta", "payload": map[string]any{"id": "main", "source": "cli"}},
		map[string]any{"type": "event_msg", "payload": map[string]any{"type": "item_completed", "thread_id": "main", "turn_id": "t", "item": map[string]any{"type": "AgentMessage", "id": "a", "content": []any{map[string]any{"type": "Text", "text": text.String()}}}}})
	t.Setenv("AGENT_TRANSCRIPT_OPEN_FILES", path)
	return path
}

func openReader(t *testing.T, s *server, name string, viewArgs ...string) string {
	t.Helper()
	args := append([]string{"--reader", name}, viewArgs...)
	stdout, stderr, err := s.launch(args...)
	if err != nil || stdout != "" || stderr != "" {
		t.Fatalf("%s launch: %q %q %v", name, stdout, stderr, err)
	}
	panes := s.panes()
	if len(panes) != 2 {
		t.Fatal("reader pane missing", panes)
	}
	pane := panes[0]
	if pane == s.source {
		pane = panes[1]
	}
	return pane
}
func TestMarkdownReadersStartAtBottomAndRemainNavigable(t *testing.T) {
	for _, name := range []string{"leaf", "glow"} {
		t.Run(name, func(t *testing.T) {
			if _, err := exec.LookPath(name); err != nil {
				t.Skipf("%s is not installed", name)
			}
			readerDocument(t, 60)
			s := newServer(t, "codex")
			pane := openReader(t, s, name, "--position", "right", "--size", "50%")
			// No user key is sent before asserting the initial bottom position.
			s.wait(func() bool { return strings.Contains(s.capture(pane), "END-OF-TRANSCRIPT") })
			if strings.Contains(s.capture(pane), "Reader heading") {
				t.Fatal("long document opened at top")
			}
			if s.tmux("display-message", "-p", "-t", "test", "#{pane_id}") != pane {
				t.Fatal("reader not focused")
			}
			if s.tmux("show-option", "-pqv", "-t", pane, startupKeyOption) != "" {
				t.Fatal("startup metadata not cleared")
			}
			s.clean()
			s.tmux("send-keys", "-t", pane, "g")
			s.wait(func() bool { return strings.Contains(s.capture(pane), "Reader heading") })
			if strings.Contains(s.capture(pane), "**Bold text**") {
				t.Fatal("Markdown was not rendered")
			}
			s.tmux("send-keys", "-t", s.source, "READER-INPUT", "Enter")
			s.wait(func() bool { return strings.Contains(s.capture(s.source), "ECHO:READER-INPUT") })
			screen := s.capture(pane)
			if !strings.Contains(screen, "Reader heading") || strings.Contains(screen, "READER-INPUT") {
				t.Fatal("startup navigation repeated or snapshot mutated")
			}
			s.tmux("send-keys", "-t", pane, "q")
			s.wait(func() bool { return len(s.panes()) == 1 })
			s.clean()
		})
	}
}

func TestShortMarkdownReadersStayOpen(t *testing.T) {
	for _, name := range []string{"leaf", "glow"} {
		t.Run(name, func(t *testing.T) {
			if _, err := exec.LookPath(name); err != nil {
				t.Skipf("%s is not installed", name)
			}
			readerDocument(t, 0)
			s := newServer(t, "codex")
			pane := openReader(t, s, name)
			s.wait(func() bool { return strings.Contains(s.capture(pane), "END-OF-TRANSCRIPT") })
			s.tmux("kill-pane", "-t", pane)
			s.clean()
		})
	}
}
func TestReaderSelectionValidationAndConfiguration(t *testing.T) {
	for _, args := range [][]string{{"--reader"}, {"--reader", "mdcat"}, {"--reader", "mdt"}, {"--reader", "cat"}, {"--reader", "leaf; touch injected"}} {
		if _, err := parse(args); err == nil {
			t.Fatalf("unsupported reader accepted: %v", args)
		}
	}
	got, err := parse([]string{"--reader=glow", "%1"})
	if err != nil || got.reader != "glow" {
		t.Fatal(got, err)
	}
	s := newServer(t)
	s.tmux("set-option", "-g", "@agent_transcript_reader", "glow")
	s.loadPlugin()
	if s.tmux("show-option", "-gqv", "@agent_transcript_reader") != "glow" {
		t.Fatal("user reader preference overwritten")
	}
	for _, table := range []string{"prefix", "copy-mode", "copy-mode-vi"} {
		if !strings.Contains(s.tmux("list-keys", "-T", table), "@agent_transcript_reader") {
			t.Fatal("binding does not read user preference")
		}
	}
}

func TestConfiguredGlowReaderIsUsedByBindingCommand(t *testing.T) {
	if _, err := exec.LookPath("glow"); err != nil {
		t.Skip("glow is not installed")
	}
	t.Setenv("PAGER", "cat")
	t.Setenv("GLOW_PAGER", "true")
	readerDocument(t, 0)
	s := newServer(t, "codex")
	s.tmux("set-option", "-g", "@agent_transcript_command", testBinary)
	s.tmux("set-option", "-g", "@agent_transcript_reader", "glow")
	s.tmux("run-shell", "-t", s.source, "#{q:@agent_transcript_command} --reader=#{q:@agent_transcript_reader} '#{pane_id}'")
	panes := s.panes()
	if len(panes) != 2 {
		t.Fatal("configured reader did not open", panes)
	}
	pane := panes[0]
	if pane == s.source {
		pane = panes[1]
	}
	s.wait(func() bool { return strings.Contains(s.capture(pane), "END-OF-TRANSCRIPT") })
	if s.tmux("display-message", "-p", "-t", pane, "#{pane_current_command}") != "glow" {
		t.Fatal("binding ignored reader setting")
	}
	s.tmux("send-keys", "-t", pane, "q")
	s.wait(func() bool { return len(s.panes()) == 1 })
	s.clean()
}

func TestMarkdownReaderBottomStartAndQuietBoundariesWithClient(t *testing.T) {
	for _, name := range []string{"leaf", "glow"} {
		t.Run(name, func(t *testing.T) {
			if _, err := exec.LookPath(name); err != nil {
				t.Skipf("%s is not installed", name)
			}
			readerDocument(t, 60)
			s := newServer(t, "codex")
			tty, _ := attachTestClient(t, s)
			pane := openReader(t, s, name)
			s.wait(func() bool { return strings.Contains(s.capture(pane), "END-OF-TRANSCRIPT") })
			terminalOutput(t, tty, 100*time.Millisecond)
			for _, keys := range []string{"gkkk", "Gjjj"} {
				tty.Write([]byte(keys))
				output := terminalOutput(t, tty, 200*time.Millisecond)
				if bytes.Contains(output, []byte{7}) || bytes.Contains(output, []byte("\x1b[?5h")) {
					t.Fatal("reader rang or flashed")
				}
			}
			tty.Write([]byte("q"))
			s.wait(func() bool { return len(s.panes()) == 1 })
			s.clean()
		})
	}
}

func TestExplicitRefreshRecoversResizedReader(t *testing.T) {
	for _, name := range []string{"leaf", "glow"} {
		t.Run(name, func(t *testing.T) {
			if _, err := exec.LookPath(name); err != nil {
				t.Skipf("%s unavailable", name)
			}
			readerDocument(t, 60)
			s := newServer(t, "codex")
			pane := openReader(t, s, name)
			s.tmux("resize-window", "-t", "test", "-x", "130", "-y", "36")
			stdout, stderr, err := s.launch("--reader", name)
			if err != nil || stdout != "" || stderr != "" {
				t.Fatalf("refresh after resize: %q %q %v", stdout, stderr, err)
			}
			if len(s.panes()) != 2 {
				t.Fatal("refresh created duplicate pane")
			}
			s.wait(func() bool { return strings.Contains(s.capture(pane), "END-OF-TRANSCRIPT") })
			s.tmux("send-keys", "-t", pane, "q")
			s.wait(func() bool { return len(s.panes()) == 1 })
			s.clean()
		})
	}
}
