package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

func attachTestClient(t *testing.T, s *server) (*os.File, string) {
	t.Helper()
	cmd := exec.Command("tmux", "-S", s.socket, "attach-session", "-t", "test")
	cmd.Env = s.env
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 30, Cols: 160})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.rawTmux("kill-server")
		tty.Close()
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			cmd.Process.Kill()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Error("notice client did not exit")
			}
		}
	})
	terminalOutput(t, tty, 200*time.Millisecond)
	for _, line := range strings.Split(s.tmux("list-clients", "-F", "#{client_pid} #{client_name}"), "\n") {
		pid, name, ok := strings.Cut(line, " ")
		if ok && pid == strconv.Itoa(cmd.Process.Pid) {
			return tty, name
		}
	}
	t.Fatal("attached notice client not found")
	return nil, ""
}

func malformedCodexServer(t *testing.T) *server {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rollout-main.jsonl")
	if err := os.WriteFile(path, []byte("not json\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_TRANSCRIPT_OPEN_FILES", path)
	return newServer(t, "codex")
}
func waitForNotice(t *testing.T, tty *os.File) []byte {
	t.Helper()
	var output []byte
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		output = append(output, terminalOutput(t, tty, 100*time.Millisecond)...)
		if bytes.Contains(output, []byte("Agent Transcript:")) {
			return output
		}
	}
	t.Fatalf("transient notice not displayed: %q", output)
	return nil
}

func TestBindingNoticeExpiresWithoutChangingPaneOrClient(t *testing.T) {
	s := malformedCodexServer(t)
	s.tmux("set-option", "-g", "status-left", "BASE-STATUS ")
	s.tmux("set-option", "-g", "status-left-length", "40")
	s.loadPlugin()
	s.tmux("set-option", "-g", "@agent_transcript_command", testBinary)
	first, _ := attachTestClient(t, s)
	second, _ := attachTestClient(t, s)
	terminalOutput(t, first, 200*time.Millisecond)
	if _, err := first.Write([]byte{2, 'P'}); err != nil {
		t.Fatal(err)
	}
	shown := waitForNotice(t, first)
	if bytes.Contains(shown, []byte{7}) {
		t.Fatal("notice rang terminal bell")
	}
	other := terminalOutput(t, second, 100*time.Millisecond)
	if bytes.Contains(other, []byte("Agent Transcript:")) {
		t.Fatal("notice leaked to other client")
	}
	if len(s.panes()) != 1 || s.tmux("display-message", "-p", "-t", "test", "#{pane_id}") != s.source {
		t.Fatal("notice changed pane layout or focus")
	}
	if s.tmux("display-message", "-p", "-t", s.source, "#{pane_in_mode}") != "0" {
		t.Fatal("run-shell output overlay appeared")
	}
	expired := terminalOutput(t, first, 3*time.Second)
	if !bytes.Contains(expired, []byte("BASE-STATUS")) {
		t.Fatalf("status was not restored automatically: %q", expired)
	}
	s.clean()
	first.Write([]byte{2, 'P'})
	waitForNotice(t, first)
	first.Write([]byte("TYPE-OK\r"))
	s.wait(func() bool { return strings.Contains(s.capture(s.source), "ECHO:TYPE-OK") })
}

func TestCopyModeNoticeDoesNotExitCopyMode(t *testing.T) {
	for _, mode := range []string{"emacs", "vi"} {
		t.Run(mode, func(t *testing.T) {
			s := malformedCodexServer(t)
			s.tmux("set-window-option", "-t", "test", "mode-keys", mode)
			s.loadPlugin()
			s.tmux("set-option", "-g", "@agent_transcript_command", testBinary)
			tty, _ := attachTestClient(t, s)
			s.tmux("copy-mode", "-t", s.source)
			terminalOutput(t, tty, 100*time.Millisecond)
			tty.Write([]byte("P"))
			waitForNotice(t, tty)
			if len(s.panes()) != 1 || s.tmux("display-message", "-p", "-t", s.source, "#{pane_in_mode}") != "1" {
				t.Fatal("copy-mode state changed")
			}
			s.clean()
		})
	}
}

func TestNotificationModePreservesActionableErrors(t *testing.T) {
	s := newServer(t)
	_, stderr, err := s.launch("--notify-client", "missing-client", "%999999")
	if err == nil || !strings.Contains(stderr, "notification failed") {
		t.Fatalf("notification failure hid original error: %s %v", stderr, err)
	}
	tty, client := attachTestClient(t, s)
	stdout, stderr, err := s.launch("--notify-client", client)
	if err != nil || stdout != "" || stderr != "" {
		t.Fatalf("unsupported foreground notification failed: %q %q %v", stdout, stderr, err)
	}
	shown := waitForNotice(t, tty)
	if !bytes.Contains(shown, []byte("Run this from a supported agent pane.")) {
		t.Fatalf("unsupported foreground notice missing: %q", shown)
	}
	if len(s.panes()) != 1 || s.tmux("display-message", "-p", "-t", "test", "#{pane_id}") != s.source {
		t.Fatal("unsupported foreground changed pane layout or focus")
	}
	if s.tmux("display-message", "-p", "-t", s.source, "#{pane_in_mode}") != "0" {
		t.Fatal("unsupported foreground changed pane mode")
	}
	s.clean()
	_, stderr, err = s.launch()
	if err == nil || !strings.Contains(stderr, "Run this from a supported agent pane.") {
		t.Fatalf("plain unsupported launch diagnostic: %q %v", stderr, err)
	}
	if len(s.panes()) != 1 {
		t.Fatalf("plain unsupported launch opened a viewer: %v", s.panes())
	}
	s.clean()
}
func TestNotifyClientOptionValidation(t *testing.T) {
	for _, args := range [][]string{{"--notify-client"}, {"--notify-client="}} {
		if _, err := parse(args); err == nil {
			t.Fatalf("accepted missing client: %v", args)
		}
	}
	got, err := parse([]string{"--notify-client=/dev/ttys123", "%1"})
	if err != nil || got.notifyClient != "/dev/ttys123" || got.target != "%1" {
		t.Fatal(fmt.Sprint(got), err)
	}
}
func TestParserFailureNoticeDoesNotCreateOutputOverlay(t *testing.T) {
	s := malformedCodexServer(t)
	tty, client := attachTestClient(t, s)
	stdout, stderr, err := s.launch("--notify-client", client)
	if err != nil || stdout != "" || stderr != "" {
		t.Fatalf("notice launch: %q %q %v", stdout, stderr, err)
	}
	output := terminalOutput(t, tty, 200*time.Millisecond)
	if !bytes.Contains(output, []byte("Agent Transcript:")) {
		t.Fatalf("missing parser error notice: %q", output)
	}
	if len(s.panes()) != 1 || s.tmux("display-message", "-p", "-t", s.source, "#{pane_in_mode}") != "0" {
		t.Fatal("parser error changed layout or mode")
	}
	_, stderr, err = s.launch()
	if err == nil || !strings.Contains(strings.ToLower(stderr), "json") {
		t.Fatalf("CLI diagnostic lost: %s %v", stderr, err)
	}
	s.clean()
}
