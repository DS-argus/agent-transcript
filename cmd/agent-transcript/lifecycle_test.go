package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestParseLayoutOptionsAndRejectsRemovedActions(t *testing.T) {
	o, err := parse([]string{"--position", "left", "--size=40%", "--focus", "off", "%1"})
	if err != nil {
		t.Fatal(err)
	}
	if o.position != "left" || o.size != "40%" || o.focus != "off" || o.target != "%1" {
		t.Fatalf("parsed options = %+v", o)
	}
	for _, args := range [][]string{{"--start", "top"}, {"--start", "bottom"}, {"--refresh"}, {"--reuse", "on"}, {"--reuse=off"}} {
		if _, err := parse(args); err == nil {
			t.Fatalf("removed options accepted: %v", args)
		}
	}
}

func hasPane(panes []string, want string) bool {
	for _, pane := range panes {
		if pane == want {
			return true
		}
	}
	return false
}
func TestOrdinaryLaunchRefreshesMatchingViewer(t *testing.T) {
	path := readerDocument(t, 60)
	s := newServer(t, "codex")
	pane := openReader(t, s, "leaf")
	s.tmux("send-keys", "-t", pane, "g")
	s.wait(func() bool { return strings.Contains(s.capture(pane), "Reader heading") })
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := bytes.ReplaceAll(original, []byte("END-OF-TRANSCRIPT"), []byte("UPDATED-END"))
	if err := os.WriteFile(path, updated, 0600); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := s.launch(); err != nil {
		t.Fatal(stderr, err)
	}
	s.wait(func() bool { return strings.Contains(s.capture(pane), "UPDATED-END") })
	if panes := s.panes(); len(panes) != 2 || !hasPane(panes, pane) {
		t.Fatalf("ordinary launch replaced or duplicated viewer: %v", panes)
	}
	if err := os.WriteFile(path, []byte("broken\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.launch(); err == nil {
		t.Fatal("corrupt document accepted")
	}
	if !strings.Contains(s.capture(pane), "UPDATED-END") {
		t.Fatal("failed refresh destroyed old view")
	}
	if err := os.WriteFile(path, updated, 0600); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := s.launch(pane); err != nil {
		t.Fatal(stderr, err)
	}
	s.wait(func() bool { return strings.Contains(s.capture(pane), "UPDATED-END") })
	if _, err := exec.LookPath("glow"); err == nil {
		if _, stderr, err := s.launch("--reader", "glow", pane); err != nil {
			t.Fatal(stderr, err)
		}
		s.wait(func() bool { return strings.Contains(s.capture(pane), "UPDATED-END") })
		if s.tmux("display-message", "-p", "-t", pane, "#{pane_current_command}") != "glow" {
			t.Fatal("reader not switched")
		}
	}
	if panes := s.panes(); len(panes) != 2 || !hasPane(panes, pane) {
		t.Fatalf("refresh changed pane set: %v", panes)
	}
	s.clean()
}

func TestConcurrentOpenSharesSingleViewer(t *testing.T) {
	readerDocument(t, 30)
	s := newServer(t, "codex")
	type result struct {
		stderr string
		err    error
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() { _, stderr, err := s.launch(); results <- result{stderr, err} }()
	}
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err != nil {
			t.Fatal(r.stderr, r.err)
		}
	}
	if len(s.panes()) != 2 {
		t.Fatal("concurrent launch created duplicates")
	}
	s.clean()
}

func TestSessionSeparation(t *testing.T) {
	path := readerDocument(t, 30)
	s := newServer(t, "codex")
	pane := openReader(t, s, "leaf")
	if _, stderr, err := s.launch(); err != nil {
		t.Fatal(stderr, err)
	}
	if panes := s.panes(); len(panes) != 2 || !hasPane(panes, pane) {
		t.Fatalf("same session did not refresh existing viewer: %v", panes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.ReplaceAll(data, []byte(`"thread_id":"main"`), []byte(`"thread_id":"other"`))
	if err := os.WriteFile(path, bytes.ReplaceAll(data, []byte(`"id":"main"`), []byte(`"id":"other"`)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := s.launch(); err != nil {
		t.Fatal(stderr, err)
	}
	if len(s.panes()) != 3 {
		t.Fatal("different session reused old viewer")
	}
	s.clean()
}
