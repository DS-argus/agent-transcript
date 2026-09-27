package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"agent-transcript/internal/reader"

	"golang.org/x/sys/unix"
)

func viewerCommand(path, readerName string) (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return quote(executable) + " _view " + quote(path) + " " + quote(readerName), nil
}

func cleanupOwnedViewer(ctx context.Context, pane string, identity snapshotIdentity) {
	state, err := inspectPane(ctx, pane)
	if err != nil || state.owner != "1" || state.source != identity.source || state.sourcePID != identity.sourcePID || state.signature != identity.signature() {
		return
	}
	_, _ = tmux(ctx, "kill-pane", "-t", pane)
}

func waitViewer(ctx context.Context, o options, identity snapshotIdentity, snapshot preparedSnapshot, viewer, originalFocus string) error {
	started := false
	defer func() {
		if !started {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			cleanupOwnedViewer(cleanupCtx, viewer, identity)
			if originalFocus != "" {
				_, _ = tmux(cleanupCtx, "select-pane", "-t", originalFocus)
			}
		}
	}()

	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(snapshot.path); errors.Is(err, os.ErrNotExist) {
			key, ready, readyErr := readerReady(ctx, viewer, o.reader, snapshot.anchor)
			if readyErr != nil {
				return readyErr
			}
			if ready {
				if _, err := tmux(ctx, "send-keys", "-t", viewer, "-l", key); err != nil {
					return err
				}
				if _, err := tmux(ctx, "set-option", "-p", "-u", "-t", viewer, startupKeyOption); err != nil {
					return err
				}
				// Readers may set their own title during initialization.
				title := "Transcript of " + identity.source
				if identity.harness == "screen" {
					title = "Screen capture of " + identity.source
				}
				if _, err := tmux(ctx, "select-pane", "-t", viewer, "-T", title); err != nil {
					return err
				}
				if o.focus == "on" {
					if _, err := tmux(ctx, "select-pane", "-t", viewer); err != nil {
						return err
					}
				} else if originalFocus != "" {
					_, _ = tmux(ctx, "select-pane", "-t", originalFocus)
				}
				started = true
				return nil
			}
		} else if err != nil {
			return err
		}
		dead, err := tmux(ctx, "display-message", "-p", "-t", viewer, "#{pane_dead}")
		if err != nil {
			return err
		}
		if dead != "0" {
			return fmt.Errorf("viewer exited before opening its input")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("viewer did not start within 10 seconds")
		case <-ticker.C:
		}
	}
}

func refreshViewer(ctx context.Context, o options, identity snapshotIdentity, snapshot preparedSnapshot, viewer, originalFocus string) error {
	command, err := viewerCommand(snapshot.path, o.reader)
	if err != nil {
		return err
	}
	if _, err := tmux(ctx, "respawn-pane", "-k", "-t", viewer, command); err != nil {
		return err
	}
	if err := markViewer(ctx, viewer, identity, o.reader); err != nil {
		return err
	}
	return waitViewer(ctx, o, identity, snapshot, viewer, originalFocus)
}

func open(ctx context.Context, o options) error {
	if o.target == "" {
		o.target = os.Getenv("TMUX_PANE")
	}
	if o.target == "" {
		return fmt.Errorf("run inside tmux or specify a target pane")
	}
	if o.focus == "" {
		o.focus = "on"
	}
	if _, err := reader.Prepare(o.reader, os.Environ()); err != nil {
		return err
	}
	source, target, err := sourceForTarget(ctx, o.target)
	if err != nil {
		return err
	}
	return withSourceLock(ctx, source.id, func() error {
		// A lock waiter may have crossed a source process switch. Re-read the
		// pane before resolving the session and refuse to act on a stale PID.
		current, err := inspectPane(ctx, source.id)
		if err != nil {
			return err
		}
		if current.dead != "0" || current.pid == "" {
			return fmt.Errorf("source pane %s is unavailable", source.id)
		}
		if source.pid != current.pid {
			return fmt.Errorf("source pane %s changed process; invoke again from the live source pane", source.id)
		}
		source = current
		initialFocus, err := activePane(ctx, source.window)
		if err != nil {
			return err
		}
		identity, err := resolveIdentity(source)
		if err != nil {
			return err
		}
		if target.owner == "1" && target.signature != identity.signature() {
			return fmt.Errorf("source session changed; open from the agent pane")
		}
		viewers, err := matchingViewers(ctx, identity)
		if err != nil {
			return err
		}
		snapshot, err := writeSnapshot(ctx, identity)
		if err != nil {
			return err
		}
		defer snapshot.cleanup()
		if len(viewers) > 0 {
			return refreshViewer(ctx, o, identity, snapshot, viewers[0].id, initialFocus)
		}
		command, err := viewerCommand(snapshot.path, o.reader)
		if err != nil {
			return err
		}
		args := append([]string(nil), splitViewArgs(o)...)
		args = append(args, "-t", identity.source, "-P", "-F", "#{pane_id}", command)
		viewer, err := tmux(ctx, append([]string{"split-window"}, args...)...)
		if err != nil {
			return err
		}
		if err := markViewer(ctx, viewer, identity, o.reader); err != nil {
			_, _ = tmux(ctx, "kill-pane", "-t", viewer)
			return err
		}
		return waitViewer(ctx, o, identity, snapshot, viewer, initialFocus)
	})
}

func view(ctx context.Context, path, readerName string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := os.Remove(path); err != nil {
		return err
	}
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return fmt.Errorf("viewer requires a tmux pane")
	}
	// reader 종료할 때, 죽은 pane 남지 않도록
	if _, err := tmux(ctx, "set-option", "-p", "-t", pane, "remain-on-exit", "off"); err != nil {
		return err
	}
	command, err := reader.Prepare(readerName, os.Environ())
	if err != nil {
		return err
	}
	if command.BottomKey == "" {
		return fmt.Errorf("reader has no bottom navigation key")
	}
	// reader 맨 아래로 이동
	if _, err := tmux(ctx, "set-option", "-p", "-t", pane, startupKeyOption, command.BottomKey); err != nil {
		return err
	}
	// 파일FD -> stdin
	if err := unix.Dup2(int(file.Fd()), 0); err != nil {
		return err
	}
	// reader execution
	return syscall.Exec(command.Executable, command.Args, command.Env)
}
