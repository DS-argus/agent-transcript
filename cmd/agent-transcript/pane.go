package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"agent-transcript/internal/reader"

	"golang.org/x/sys/unix"
)

// Pane options below are deliberately separate from the plugin configuration
// options.  They describe ownership of a viewer, not user preferences.
const (
	viewerOwnerOption     = "@agent_transcript_viewer"
	viewerSourceOption    = "@agent_transcript_source_pane"
	viewerSourcePIDOption = "@agent_transcript_source_pid"
	viewerSignatureOption = "@agent_transcript_session_signature"
	viewerReaderOption    = "@agent_transcript_viewer_reader"
)

type paneState struct {
	id, dead, pid, window, command              string
	owner, source, sourcePID, signature, reader string
}

func supportedViewerCommand(command string) bool {
	return reader.Supported(filepath.Base(strings.TrimSpace(command)))
}

func inspectPane(ctx context.Context, target string) (paneState, error) {
	value, err := tmux(ctx, "display-message", "-p", "-t", target,
		"#{pane_id}\t"+
			"#{pane_dead}\t"+
			"#{pane_pid}\t"+
			"#{window_id}\t"+
			"#{pane_current_command}\t"+
			"#{@agent_transcript_viewer}\t"+
			"#{@agent_transcript_source_pane}\t"+
			"#{@agent_transcript_source_pid}\t"+
			"#{@agent_transcript_session_signature}\t"+
			"#{@agent_transcript_viewer_reader}\t"+
			"END",
	)
	if err != nil {
		return paneState{}, err
	}
	fields := strings.Split(value, "\t")
	if len(fields) != 11 || fields[10] != "END" || fields[0] == "" {
		return paneState{}, fmt.Errorf("invalid pane metadata for %q", target)
	}
	return paneState{
		id: fields[0], dead: fields[1], pid: fields[2], window: fields[3], command: fields[4],
		owner: fields[5], source: fields[6], sourcePID: fields[7], signature: fields[8], reader: fields[9],
	}, nil
}

func activePane(ctx context.Context, window string) (string, error) {
	value, err := tmux(ctx, "list-panes", "-t", window, "-F", "#{pane_id}\t#{pane_active}")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(value, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) == 2 && fields[1] == "1" {
			return fields[0], nil
		}
	}
	return "", nil
}

// sourceForTarget maps an invocation from a viewer back to its live source.
// A pane PID check prevents a viewer from being reused after its source pane
// has been repurposed for another process/session.
func sourceForTarget(ctx context.Context, target string) (paneState, paneState, error) {
	targetPane, err := inspectPane(ctx, target)
	if err != nil {
		return paneState{}, paneState{}, err
	}
	if targetPane.dead != "0" {
		return paneState{}, paneState{}, fmt.Errorf("target pane %s is dead", targetPane.id)
	}
	if targetPane.owner != "1" || targetPane.source == "" {
		return targetPane, targetPane, nil
	}
	if targetPane.id != targetPane.source && !supportedViewerCommand(targetPane.command) {
		return paneState{}, paneState{}, fmt.Errorf("viewer pane %s no longer runs a supported reader", targetPane.id)
	}
	sourcePane, err := inspectPane(ctx, targetPane.source)
	if err != nil {
		return paneState{}, paneState{}, fmt.Errorf("viewer source pane %s is unavailable: %w", targetPane.source, err)
	}
	if sourcePane.dead != "0" {
		return paneState{}, paneState{}, fmt.Errorf("viewer source pane %s is dead", sourcePane.id)
	}
	if targetPane.sourcePID == "" || sourcePane.pid == "" || sourcePane.pid != targetPane.sourcePID {
		return paneState{}, paneState{}, fmt.Errorf("viewer source pane %s changed process; invoke from the live source pane", sourcePane.id)
	}
	return sourcePane, targetPane, nil
}

func markViewer(ctx context.Context, pane string, identity snapshotIdentity, readerName string) error {
	values := [][2]string{
		{viewerOwnerOption, "1"},
		{viewerSourceOption, identity.source},
		{viewerSourcePIDOption, identity.sourcePID},
		{viewerSignatureOption, identity.signature()},
		{viewerReaderOption, readerName},
	}
	for _, value := range values {
		if _, err := tmux(ctx, "set-option", "-p", "-t", pane, value[0], value[1]); err != nil {
			return err
		}
	}
	return nil
}

func matchingViewers(ctx context.Context, identity snapshotIdentity) ([]paneState, error) {
	panes, err := tmux(ctx, "list-panes", "-t", identity.sourceWindow, "-F",
		"#{pane_id}\t#{pane_dead}\t#{pane_pid}\t#{window_id}\t#{pane_current_command}\t#{@agent_transcript_viewer}\t#{@agent_transcript_source_pane}\t#{@agent_transcript_source_pid}\t#{@agent_transcript_session_signature}\t#{@agent_transcript_viewer_reader}\tEND")
	if err != nil {
		return nil, err
	}
	want := identity.signature()
	result := []paneState{}
	for _, line := range strings.Split(panes, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 11 || fields[10] != "END" {
			continue
		}
		pane := paneState{
			id: fields[0], dead: fields[1], pid: fields[2], window: fields[3], command: fields[4],
			owner: fields[5], source: fields[6], sourcePID: fields[7], signature: fields[8], reader: fields[9],
		}
		if pane.dead != "0" || pane.owner != "1" || pane.source != identity.source || pane.sourcePID != identity.sourcePID || pane.signature != want || pane.command != pane.reader || !supportedViewerCommand(pane.command) {
			continue
		}
		result = append(result, pane)
	}
	sort.Slice(result, func(i, j int) bool {
		a, aerr := strconv.Atoi(strings.TrimPrefix(result[i].id, "%"))
		b, berr := strconv.Atoi(strings.TrimPrefix(result[j].id, "%"))
		if aerr == nil && berr == nil && a != b {
			return a < b
		}
		return result[i].id < result[j].id
	})
	return result, nil
}

func withSourceLock(ctx context.Context, source string, fn func() error) error {
	server, err := tmux(ctx, "display-message", "-p", "#{socket_path}:#{pid}")
	if err != nil {
		return err
	}
	dir, err := cacheDirectory("locks")
	if err != nil {
		return err
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(server+":"+source)))
	fd, err := unix.Open(filepath.Join(dir, key+".lock"), unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	deadline, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if err != unix.EWOULDBLOCK && err != unix.EAGAIN {
			return err
		}
		select {
		case <-deadline.Done():
			return fmt.Errorf("waiting for source viewer lock: %w", deadline.Err())
		case <-ticker.C:
		}
	}
	defer unix.Flock(fd, unix.LOCK_UN)
	// Leave the empty lock inode in cache: removing it can split concurrent locks.
	return fn()
}
