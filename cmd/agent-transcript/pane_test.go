package main

import (
	"context"
	"strings"
	"testing"
)

func TestSupportedViewerCommandRejectsForeignOrAlteredPane(t *testing.T) {
	for _, command := range []string{"leaf", "glow", "/usr/local/bin/leaf"} {
		if !supportedViewerCommand(command) {
			t.Fatalf("supported reader rejected: %q", command)
		}
	}
	for _, command := range []string{"bash", "vim", "leaf-wrapper", "mdt", ""} {
		if supportedViewerCommand(command) {
			t.Fatalf("foreign command accepted: %q", command)
		}
	}
}

// This small tmux-facing check protects the lock's bounded-wait contract
// without requiring a live server in unit-only runs. A canceled context must
// be observable by callers rather than turning into an unbounded wait.
func TestSourceLockCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := withSourceLock(ctx, "%does-not-exist", func() error { return nil })
	if err == nil || !strings.Contains(err.Error(), "context canceled") && !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("unexpected canceled lock error: %v", err)
	}
}
