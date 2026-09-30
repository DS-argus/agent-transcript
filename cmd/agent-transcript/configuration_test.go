package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func (s *server) loadPlugin() {
	s.t.Helper()
	data, err := os.ReadFile("../../tmux/agent-transcript.tmux")
	if err != nil {
		s.t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "set -gF @agent_transcript_command ") {
			lines[i] = "set -g @agent_transcript_command " + quote(testBinary)
		}
	}
	path := filepath.Join(s.dir, "plugin.tmux")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0600); err != nil {
		s.t.Fatal(err)
	}
	s.tmux("source-file", path)
}
func TestViewOptionsValidation(t *testing.T) {
	for _, args := range [][]string{{"--position", "middle"}, {"--size", "0"}, {"--size", "100%"}, {"--size", "-4"}, {"--size", "20;kill"}, {"--focus", "yes"}, {"--reuse", "sometimes"}, {"--start", "latest"}} {
		if _, err := parse(args); err == nil {
			t.Fatalf("accepted invalid args %v", args)
		}
	}
	for _, args := range [][]string{{"--size", "12"}, {"--size", "35%"}, {"--focus", "off"}} {
		if _, err := parse(args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
}
func TestLayoutFocusBottomCombinations(t *testing.T) {
	for _, position := range []string{"right", "left", "top", "bottom"} {
		for _, focus := range []string{"on", "off"} {
			t.Run(position+"/"+focus, func(t *testing.T) {
				readerDocument(t, 0)
				s := newServer(t, "codex")
				pane := s.open("--position", position, "--size", "35%", "--focus", focus)
				expected := s.source
				if focus == "on" {
					expected = pane
				}
				if got := s.tmux("display-message", "-p", "-t", "test", "#{pane_id}"); got != expected {
					t.Fatalf("focus %s want %s", got, expected)
				}
				marker := "END-OF-TRANSCRIPT"
				s.wait(func() bool { return strings.Contains(s.capture(pane), marker) })
				coords := func(p string) (int, int) {
					f := strings.Fields(s.tmux("display-message", "-p", "-t", p, "#{pane_left} #{pane_top}"))
					x, _ := strconv.Atoi(f[0])
					y, _ := strconv.Atoi(f[1])
					return x, y
				}
				x, y := coords(pane)
				sx, sy := coords(s.source)
				if position == "right" && x <= sx || position == "left" && x >= sx || position == "top" && y >= sy || position == "bottom" && y <= sy {
					t.Fatalf("wrong layout source=%d,%d viewer=%d,%d", sx, sy, x, y)
				}
				s.close(pane)
			})
		}
	}
}
func TestDefaultLayoutUsesTopViewerAndViewerPercentage(t *testing.T) {
	readerDocument(t, 0)
	s := newServer(t, "codex")
	pane := s.open()
	s.wait(func() bool { return strings.Contains(s.capture(pane), "END-OF-TRANSCRIPT") })
	if got := s.tmux("display-message", "-p", "-t", "test", "#{pane_id}"); got != pane {
		t.Fatalf("default focus = %s, want %s", got, pane)
	}
	paneTop, err := strconv.Atoi(s.tmux("display-message", "-p", "-t", pane, "#{pane_top}"))
	if err != nil {
		t.Fatal(err)
	}
	sourceTop, err := strconv.Atoi(s.tmux("display-message", "-p", "-t", s.source, "#{pane_top}"))
	if err != nil {
		t.Fatal(err)
	}
	viewerHeight, err := strconv.Atoi(s.tmux("display-message", "-p", "-t", pane, "#{pane_height}"))
	if err != nil {
		t.Fatal(err)
	}
	sourceHeight, err := strconv.Atoi(s.tmux("display-message", "-p", "-t", s.source, "#{pane_height}"))
	if err != nil {
		t.Fatal(err)
	}
	windowHeight, err := strconv.Atoi(s.tmux("display-message", "-p", "-t", "test", "#{window_height}"))
	if err != nil {
		t.Fatal(err)
	}
	if paneTop != 0 || sourceTop <= paneTop {
		t.Fatalf("default layout is not top: viewer top=%d source top=%d", paneTop, sourceTop)
	}
	if viewerHeight != windowHeight*90/100 || viewerHeight+sourceHeight+1 != windowHeight {
		t.Fatalf("default viewer size is not 90%%: viewer=%d source=%d window=%d", viewerHeight, sourceHeight, windowHeight)
	}
	s.close(pane)
}
func TestCellSizeAndInvalidRefreshOptions(t *testing.T) {
	readerDocument(t, 0)
	s := newServer(t, "codex")
	pane := s.open("--position", "bottom", "--size", "10")
	if got := s.tmux("display-message", "-p", "-t", pane, "#{pane_height}"); got != "10" {
		t.Fatal("cell size not applied", got)
	}
	_, stderr, err := s.launch("--size", "100%")
	if err == nil || stderr == "" {
		t.Fatal("invalid refresh option accepted")
	}
	if len(s.panes()) != 2 {
		t.Fatal("invalid option destroyed viewer")
	}
	s.clean()
}
func TestConfigReloadRebindsOnlyOwnedKeys(t *testing.T) {
	s := newServer(t)
	s.loadPlugin()
	s.tmux("set-option", "-g", "@agent_transcript_key", "Y")
	s.tmux("set-option", "-g", "@agent_transcript_copy_mode_key", "C-y")
	// A user replaces the old P key after installation. Reload must keep it.
	s.tmux("bind-key", "P", "display-message", "user binding")
	s.loadPlugin()
	listing := s.tmux("list-keys", "-T", "prefix")
	if !strings.Contains(listing, "user binding") {
		t.Fatal("user replacement lost")
	}
	if strings.Contains(listing, "--refresh") || strings.Contains(listing, "--reuse") {
		t.Fatal("obsolete action flags remain in bindings")
	}
	for _, table := range []string{"copy-mode", "copy-mode-vi"} {
		if !strings.Contains(s.tmux("list-keys", "-T", table), "C-y") {
			t.Fatal("copy key not updated")
		}
	}
	s.tmux("set-option", "-g", "@agent_transcript_key", "none")
	s.tmux("set-option", "-g", "@agent_transcript_copy_mode_key", "none")
	s.loadPlugin()
	var saved []managedBinding
	if err := json.Unmarshal([]byte(s.tmux("show-option", "-gqv", bindingsOption)), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved) != 0 {
		t.Fatal("disabled keys persisted")
	}
	listing = s.tmux("list-keys", "-T", "prefix")
	if strings.Contains(listing, "@agent_transcript_command") {
		t.Fatal("old plugin key remained", listing)
	}
}
func TestConfigurationRejectsCollidingKeysBeforeMutation(t *testing.T) {
	s := newServer(t)
	s.loadPlugin()
	old := s.tmux("list-keys", "-T", "prefix")
	s.tmux("set-option", "-g", "@agent_transcript_key", "NotARealTmuxKey")
	out, stderr, err := s.launch("_configure")
	if err == nil || stderr == "" {
		t.Fatalf("%s %s %v", out, stderr, err)
	}
	if s.tmux("list-keys", "-T", "prefix") != old {
		t.Fatal("invalid config mutated bindings")
	}
}
func TestErrorNoticeCannotExecuteTmuxFormats(t *testing.T) {
	notice := errorNotice(fmt.Errorf("bad #(touch /tmp/injected) #{pane_id} #[blink] %%H\nprivate details"))
	if strings.ContainsAny(notice, "#%\n") || strings.Contains(notice, "private details") {
		t.Fatal("unsafe notice", notice)
	}
	if len([]rune(errorNotice(fmt.Errorf("%s", strings.Repeat("x", 1000))))) > 200 {
		t.Fatal("unbounded error notice")
	}
}

func TestReloadRemovesPreviouslyOwnedSeparateRefreshKey(t *testing.T) {
	s := newServer(t)
	s.loadPlugin()
	command := "previous-owned-refresh-command"
	s.tmux("bind-key", "-T", "prefix", "M-P", "run-shell", "-b", command)
	var saved []managedBinding
	if err := json.Unmarshal([]byte(s.tmux("show-option", "-gqv", bindingsOption)), &saved); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(s.tmux("list-keys", "-T", "prefix"), "\n") {
		if strings.Contains(line, command) {
			saved = append(saved, managedBinding{Table: "prefix", Key: "M-P", Command: command, Listing: strings.Join(strings.Fields(line), " ")})
		}
	}
	data, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	s.tmux("set-option", "-g", bindingsOption, string(data))
	s.loadPlugin()
	if strings.Contains(s.tmux("list-keys", "-T", "prefix"), command) {
		t.Fatal("old owned refresh key remained")
	}
	if strings.Contains(s.tmux("show-option", "-gqv", bindingsOption), "M-P") {
		t.Fatal("refresh metadata remained")
	}
}

func TestSingleKeyOpensThenRefreshesSamePane(t *testing.T) {
	path := writeJSON(t, filepath.Join(t.TempDir(), "rollout-main.jsonl"),
		map[string]any{"type": "session_meta", "payload": map[string]any{"id": "main", "source": "cli"}},
		map[string]any{"type": "event_msg", "payload": map[string]any{"type": "item_completed", "thread_id": "main", "item": map[string]any{"type": "AgentMessage", "id": "a", "content": []any{map[string]any{"type": "Text", "text": "First version"}}}}})
	t.Setenv("AGENT_TRANSCRIPT_OPEN_FILES", path)
	s := newServer(t, "codex")
	s.loadPlugin()
	tty, _ := attachTestClient(t, s)
	tty.Write([]byte{2, 'P'})
	s.wait(func() bool { return len(s.panes()) == 2 })
	pane := s.panes()[0]
	if pane == s.source {
		pane = s.panes()[1]
	}
	s.wait(func() bool { return strings.Contains(s.capture(pane), "First version") })
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(data), "First version", "Second version")), 0600); err != nil {
		t.Fatal(err)
	}
	// The same binding is invoked while the viewer, not the agent, is focused.
	tty.Write([]byte{2, 'P'})
	s.wait(func() bool { return strings.Contains(s.capture(pane), "Second version") })
	s.wait(func() bool {
		entries, err := os.ReadDir(s.snapshotRoot())
		return err == nil && len(entries) == 0
	})
	if len(s.panes()) != 2 {
		t.Fatal("second press created another pane")
	}
	s.clean()
}
