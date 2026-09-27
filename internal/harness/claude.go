package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"agent-transcript/internal/transcript"
)

// Claude writes files on demand rather than keeping its transcript descriptor
// open. Its per-PID registry supplies a session ID and UTC process birth time.
func (r Resolver) claudeSessions(pids []int) ([]Source, error) {
	var matches []Source
	for _, pid := range pids {
		registryPath := filepath.Join(r.ClaudeDir, "sessions", strconv.Itoa(pid)+".json")
		data, err := os.ReadFile(registryPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var record struct {
			PID        int    `json:"pid"`
			SessionID  string `json:"sessionId"`
			ProcStart  string `json:"procStart"`
			PIDDomain  string `json:"pidDomain"`
			Kind       string `json:"kind"`
			Entrypoint string `json:"entrypoint"`
		}
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, fmt.Errorf("invalid Claude session registry %s: %w", registryPath, err)
		}
		if record.PID != pid {
			return nil, fmt.Errorf("Claude registry PID disagrees with its filename: %s", registryPath)
		}
		if record.Kind != "interactive" || record.Entrypoint != "cli" || record.PIDDomain != runtime.GOOS {
			continue
		}
		if !validID(record.SessionID) || strings.TrimSpace(record.ProcStart) == "" {
			return nil, fmt.Errorf("Claude registry lacks a valid session ID/process start: %s", registryPath)
		}
		start, err := r.Run("ps", "-p", strconv.Itoa(pid), "-o", "lstart=")
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) && exit.ExitCode() == 1 {
				continue // process exited after the process-tree snapshot
			}
			return nil, err
		}
		if normalizeStart(string(start)) != normalizeStart(record.ProcStart) {
			continue // stale registry or reused PID, never select by PID alone
		}
		paths, err := filepath.Glob(filepath.Join(r.ClaudeDir, "projects", "*", record.SessionID+".jsonl"))
		if err != nil {
			return nil, err
		}
		if len(paths) == 0 {
			return nil, fmt.Errorf("Claude session %s has no transcript file; finish a conversation turn and retry", record.SessionID)
		}
		if len(paths) != 1 {
			return nil, fmt.Errorf("Claude session %s has %d transcript files; expected one persisted conversation", record.SessionID, len(paths))
		}
		id, err := claudeFileID(paths[0])
		if err != nil {
			return nil, err
		}
		if id != record.SessionID {
			return nil, fmt.Errorf("Claude registry and transcript session IDs disagree")
		}
		// A /resume or session switch can happen without replacing the process.
		after, err := os.ReadFile(registryPath)
		if err != nil {
			return nil, fmt.Errorf("Claude registry changed during discovery; retry: %w", err)
		}
		var current struct {
			PID       int    `json:"pid"`
			SessionID string `json:"sessionId"`
			ProcStart string `json:"procStart"`
		}
		if json.Unmarshal(after, &current) != nil || current.PID != pid || current.SessionID != record.SessionID || normalizeStart(current.ProcStart) != normalizeStart(record.ProcStart) {
			return nil, fmt.Errorf("Claude session changed during discovery; retry")
		}
		again, err := r.Run("ps", "-p", strconv.Itoa(pid), "-o", "lstart=")
		if err != nil || normalizeStart(string(again)) != normalizeStart(record.ProcStart) {
			return nil, fmt.Errorf("Claude process changed during discovery; retry")
		}
		matches = append(matches, Source{Harness: "claude", Path: paths[0]})
	}
	return matches, nil
}

func normalizeStart(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func ClaudeConfigDir() string {
	if path := os.Getenv("CLAUDE_CONFIG_DIR"); path != "" {
		return expandHome(path)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

func (r Resolver) locateClaude(pids []int) ([]Source, error) {
	matches, err := r.claudeSessions(pids)
	if err != nil {
		return nil, err
	}
	paths, err := r.openFiles(pids)
	if err != nil {
		return nil, err
	}
	root, err := filepath.EvalSymlinks(filepath.Join(r.ClaudeDir, "projects"))
	if errors.Is(err, os.ErrNotExist) {
		return matches, nil
	}
	if err != nil {
		return nil, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil {
			continue
		}
		canonical, err = filepath.Abs(canonical)
		if err != nil {
			return nil, err
		}
		relative, err := filepath.Rel(root, canonical)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		if len(strings.Split(relative, string(filepath.Separator))) != 2 {
			continue
		}
		id, err := claudeFileID(path)
		if err != nil {
			return nil, err
		}
		if filepath.Base(path) == id+".jsonl" {
			matches = append(matches, Source{"claude", path})
		}
	}
	return matches, nil
}

func recognizeClaude(records []map[string]any) (string, bool) {
	for _, record := range records {
		kind := transcript.String(record, "type")
		if (kind == "user" || kind == "assistant") && transcript.Object(record, "message") != nil && transcript.String(record, "sessionId") != "" {
			return transcript.String(record, "sessionId"), true
		}
	}
	return "", false
}

func claudeFileID(path string) (string, error) {
	records, _, err := transcript.ReadJSONL(path)
	if err != nil {
		return "", err
	}
	id, ok := recognizeClaude(records)
	if !ok {
		return "", fmt.Errorf("unrecognized Claude transcript: %s", path)
	}
	return id, nil
}
