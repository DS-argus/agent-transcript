package main

import (
	"context"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestSizeResolutionPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, agent, all, specific, explicit, want string
		wantErr                                    bool
	}{
		{name: "built-in", agent: "codex", want: "90%"},
		{name: "all", agent: "claude", all: "85%", want: "85%"},
		{name: "codex override", agent: "codex", all: "85%", specific: "70%", want: "70%"},
		{name: "claude override", agent: "claude", all: "85%", specific: "65%", want: "65%"},
		{name: "gjc override", agent: "gjc", all: "85%", specific: "55%", want: "55%"},
		{name: "cells without all", agent: "gjc", specific: "12", want: "12"},
		{name: "empty override", agent: "claude", all: "75%", specific: "", want: "75%"},
		{name: "invalid override is not fallback", agent: "claude", all: "75%", specific: "100%", wantErr: true},
		{name: "invalid all", agent: "codex", all: "0", wantErr: true},
		{name: "override ignores unused invalid all", agent: "claude", all: "bad", specific: "80%", want: "80%"},
		{name: "explicit wins", agent: "gjc", all: "75%", specific: "60%", explicit: "40%", want: "40%"},
		{name: "explicit ignores unused invalid profiles", agent: "claude", all: "bad", specific: "bad", explicit: "25", want: "25"},
		{name: "unsupported agent", agent: "leaf", all: "75%", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newServer(t)
			t.Setenv("TMUX", s.socket+",0,0")
			// The removed option must not act as a compatibility fallback.
			s.tmux("set-option", "-g", "@agent_transcript_size", "10")
			if tc.all != "" {
				s.tmux("set-option", "-g", "@agent_transcript_size_all", tc.all)
			}
			if tc.agent != "leaf" {
				s.tmux("set-option", "-g", "@agent_transcript_size_"+tc.agent, tc.specific)
			}
			o := defaultOptions()
			if tc.explicit != "" {
				var err error
				o, err = parse([]string{"--size", tc.explicit})
				if err != nil {
					t.Fatal(err)
				}
			}
			got, err := resolveViewSize(context.Background(), o, tc.agent)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("invalid configuration accepted: %+v", got)
				}
				return
			}
			if err != nil || got.size != tc.want {
				t.Fatalf("size=%q error=%v, want %q", got.size, err, tc.want)
			}
		})
	}
}

func TestSizeCLIExplicitnessAndInspectionFailure(t *testing.T) {
	for _, arg := range []string{"--size=40%", "--size=12"} {
		o, err := parse([]string{arg})
		if err != nil {
			t.Fatal(err)
		}
		if !o.sizeExplicit {
			t.Fatal("explicit CLI size lost")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		// An explicit argument does not depend on any tmux option read.
		if got, err := resolveViewSize(ctx, o, "claude"); err != nil || got.size != o.size {
			t.Fatalf("explicit override inspected tmux: %+v %v", got, err)
		}
	}
	o, err := parse(nil)
	if err != nil || o.sizeExplicit {
		t.Fatalf("default marked explicit: %+v %v", o, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := resolveViewSize(ctx, o, "claude"); err == nil {
		t.Fatal("failed option read silently became default")
	}
}

func sizedAgentServer(t *testing.T, name string) (*server, string) {
	t.Helper()
	switch name {
	case "claude":
		return autoFixtureServer(t, name, claudeMessage("main", "a", "Sized agent answer"))
	case "codex":
		return autoFixtureServer(t, name,
			map[string]any{"type": "session_meta", "payload": map[string]any{"id": "main", "source": "cli"}},
			map[string]any{"type": "event_msg", "payload": map[string]any{"type": "item_completed", "thread_id": "main", "item": map[string]any{"type": "AgentMessage", "id": "a", "content": []any{map[string]any{"type": "Text", "text": "Sized agent answer"}}}}})
	case "gjc":
		if runtime.GOOS != "darwin" {
			t.Skip("GJC foreground identity is macOS-only")
		}
		return autoFixtureServer(t, name, map[string]any{"type": "session", "version": 5, "id": "main"},
			map[string]any{"type": "message", "id": "a", "parentId": nil, "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "Sized agent answer"}}}})
	default:
		t.Fatalf("unsupported test agent %s", name)
		return nil, ""
	}
}
func viewerWidth(t *testing.T, s *server, pane string) int {
	t.Helper()
	width, err := strconv.Atoi(s.tmux("display-message", "-p", "-t", pane, "#{pane_width}"))
	if err != nil {
		t.Fatal(err)
	}
	return width
}
func TestAgentSizeControlsNewPaneGeometry(t *testing.T) {
	for _, name := range []string{"codex", "claude", "gjc"} {
		t.Run(name, func(t *testing.T) {
			s, _ := sizedAgentServer(t, name)
			initialWidth := viewerWidth(t, s, s.source)
			defaultPane := s.open("--position", "right")
			if got := viewerWidth(t, s, defaultPane); got != initialWidth*90/100 {
				t.Fatalf("%s default width %d, want 90%% of %d", name, got, initialWidth)
			}
			s.close(defaultPane)
			s.tmux("set-option", "-g", "@agent_transcript_size_all", "60")
			for _, other := range []string{"codex", "claude", "gjc"} {
				s.tmux("set-option", "-g", "@agent_transcript_size_"+other, "40")
			}
			s.tmux("set-option", "-g", "@agent_transcript_size_"+name, "45")
			pane := s.open("--position", "right")
			if got := viewerWidth(t, s, pane); got != 45 {
				t.Fatalf("%s override width %d, want 45", name, got)
			}
			s.close(pane)
			pane = s.open("--position", "right", "--size", "55")
			if got := viewerWidth(t, s, pane); got != 55 {
				t.Fatalf("explicit width %d, want 55", got)
			}
			s.close(pane)
			s.tmux("set-option", "-g", "@agent_transcript_size_"+name, "")
			pane = s.open("--position", "right")
			if got := viewerWidth(t, s, pane); got != 60 {
				t.Fatalf("empty override width %d, want 60", got)
			}
			s.close(pane)
			s.tmux("set-option", "-gu", "@agent_transcript_size_"+name)
			s.tmux("set-option", "-g", "@agent_transcript_size_all", "50%")
			total := viewerWidth(t, s, s.source)
			pane = s.open("--position", "right")
			if got := viewerWidth(t, s, pane); got != total/2 {
				t.Fatalf("all percentage width %d, want %d", got, total/2)
			}
			s.close(pane)
		})
	}
}

func TestInvalidSizeProfilesDoNotMutateBindingsOrOpenPanes(t *testing.T) {
	s, _ := sizedAgentServer(t, "claude")
	s.loadPlugin()
	old := s.tmux("list-keys", "-T", "prefix")
	for _, name := range []string{"all", "codex", "claude", "gjc"} {
		key := "@agent_transcript_size_" + name
		s.tmux("set-option", "-g", key, "100%")
		_, stderr, err := s.launch("_configure")
		if err == nil || !strings.Contains(stderr, "size") {
			t.Fatalf("invalid %s accepted: %s %v", name, stderr, err)
		}
		if s.tmux("list-keys", "-T", "prefix") != old {
			t.Fatal("invalid profile mutated bindings")
		}
		s.tmux("set-option", "-gu", key)
	}
	s.tmux("set-option", "-g", "@agent_transcript_size_all", "60")
	s.tmux("set-option", "-g", "@agent_transcript_size_claude", "100%")
	_, stderr, err := s.launch("--position", "right")
	if err == nil || !strings.Contains(stderr, "size") || len(s.panes()) != 1 {
		t.Fatalf("invalid active override fell back or opened viewer: %s %v", stderr, err)
	}
}

func TestBindingUsesSourceProfileAndPreservesRefreshGeometry(t *testing.T) {
	s, path := sizedAgentServer(t, "claude")
	s.loadPlugin()
	s.tmux("set-option", "-g", "@agent_transcript_position", "right")
	s.tmux("set-option", "-g", "@agent_transcript_size_all", "70")
	s.tmux("set-option", "-g", "@agent_transcript_size_claude", "50")
	for _, table := range []string{"prefix", "copy-mode", "copy-mode-vi"} {
		for _, line := range strings.Split(s.tmux("list-keys", "-T", table), "\n") {
			if strings.Contains(line, "agent_transcript_command") && strings.Contains(line, "--size") {
				t.Fatal("binding fixes size before agent detection", line)
			}
		}
	}
	tty, _ := attachTestClient(t, s)
	if _, err := tty.Write([]byte{2, 'P'}); err != nil {
		t.Fatal(err)
	}
	s.wait(func() bool { return len(s.panes()) == 2 })
	var pane string
	for _, id := range s.panes() {
		if id != s.source {
			pane = id
		}
	}
	s.wait(func() bool { return strings.Contains(s.capture(pane), "Sized agent answer") })
	if got := viewerWidth(t, s, pane); got != 50 {
		t.Fatalf("binding size %d, want 50", got)
	}
	s.tmux("resize-pane", "-t", pane, "-x", "65")
	width := viewerWidth(t, s, pane)
	s.tmux("set-option", "-g", "@agent_transcript_size_claude", "40")
	writeJSON(t, path, claudeMessage("main", "a", "Updated sized answer"))
	// Pressing from the Leaf viewer still selects the Claude source's profile.
	if _, err := tty.Write([]byte{2, 'P'}); err != nil {
		t.Fatal(err)
	}
	s.wait(func() bool { return strings.Contains(s.capture(pane), "Updated sized answer") })
	if len(s.panes()) != 2 || viewerWidth(t, s, pane) != width {
		t.Fatal("refresh changed resized geometry or created another viewer")
	}
	s.wait(func() bool {
		entries, err := os.ReadDir(s.snapshotRoot())
		return err == nil && len(entries) == 0
	})
	s.close(pane)
}
