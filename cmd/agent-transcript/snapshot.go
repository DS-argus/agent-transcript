package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"agent-transcript/internal/harness"
)

type snapshotIdentity struct {
	source       string
	sourcePID    string
	sourceWindow string
	path         string
	harness      string
	session      string
}

func (i snapshotIdentity) signature() string {
	// Keep the tmux option short and opaque.  All identity components are
	// included before hashing, so a path replacement or harness/session switch
	// cannot accidentally match an older viewer.
	value := strings.Join([]string{
		i.path, i.harness, i.session, i.source, i.sourcePID, i.sourceWindow,
	}, "\x1f")
	return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
}

func canonicalPath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return filepath.Abs(resolved)
}

func resolveIdentity(source paneState) (snapshotIdentity, error) {
	identity := snapshotIdentity{
		source: source.id, sourcePID: source.pid, sourceWindow: source.window,
	}
	pid, err := strconv.Atoi(source.pid)
	if err != nil || pid <= 0 {
		return snapshotIdentity{}, fmt.Errorf("invalid source pane PID %q", source.pid)
	}
	located, err := (harness.Resolver{}).Resolve(pid)
	if err != nil {
		if errors.Is(err, harness.ErrUnsupportedForeground) {
			return snapshotIdentity{}, fmt.Errorf("Run this from a supported agent pane.")
		}
		return snapshotIdentity{}, err
	}
	canonical, err := canonicalPath(located.Path)
	if err != nil {
		return snapshotIdentity{}, err
	}
	identity.path = canonical
	identity.harness = located.Harness
	kind, session, err := harness.DetectFile(canonical)
	if err != nil {
		return snapshotIdentity{}, err
	}
	if kind != identity.harness {
		return snapshotIdentity{}, fmt.Errorf("transcript format is %s, not %s", kind, identity.harness)
	}
	identity.session = session
	return identity, nil
}

type preparedSnapshot struct {
	path      string
	anchor    string
	directory string
}

func (s preparedSnapshot) cleanup() { _ = os.RemoveAll(s.directory) }

func writeSnapshot(identity snapshotIdentity) (preparedSnapshot, error) {
	root, err := cacheDirectory("snapshots")
	if err != nil {
		return preparedSnapshot{}, err
	}
	directory, err := os.MkdirTemp(root, "snapshot-")
	if err != nil {
		return preparedSnapshot{}, err
	}
	result := preparedSnapshot{directory: directory, path: filepath.Join(directory, "snapshot.md")}
	file, err := os.OpenFile(result.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		result.cleanup()
		return preparedSnapshot{}, err
	}
	doc, writeErr := harness.RenderFile(identity.harness, identity.path)
	if writeErr == nil && (doc.Harness != identity.harness || doc.SessionID != identity.session) {
		writeErr = fmt.Errorf("source transcript identity changed during snapshot; retry")
	}
	if writeErr == nil {
		_, writeErr = io.WriteString(file, doc.Markdown)
	}
	closeErr := file.Close()
	if writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		result.cleanup()
		return preparedSnapshot{}, writeErr
	}
	result.anchor, err = documentAnchor(result.path)
	if err != nil {
		result.cleanup()
		return preparedSnapshot{}, err
	}
	return result, nil
}
