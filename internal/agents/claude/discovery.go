// Package claude discovers owned foreground sessions and renders their public messages.
package claude

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

	"agent-transcript/internal/agents"
	"agent-transcript/internal/process"
	"agent-transcript/internal/transcript"
)

type Resolver struct {
	Dir string
	Run process.Runner
}

type sessionRegistry struct {
	PID        int    `json:"pid"`
	SessionID  string `json:"sessionId"`
	ProcStart  string `json:"procStart"`
	PIDDomain  string `json:"pidDomain"`
	Kind       string `json:"kind"`
	Entrypoint string `json:"entrypoint"`
}

// Locate uses only Claude's PID registry. JSONL descriptors are normally closed;
// neither open files nor transcript timestamps prove ownership.
func (r Resolver) Locate(pids []int) (agents.Source, error) {
	if err := agents.ValidatePIDs("claude", pids); err != nil {
		return agents.Source{}, err
	}
	if r.Dir == "" {
		r.Dir = ConfigDir()
	}
	if r.Run == nil {
		r.Run = process.Run
	}
	var matches []agents.Source
	for _, pid := range pids {
		registryPath := filepath.Join(r.Dir, "sessions", strconv.Itoa(pid)+".json")
		data, err := os.ReadFile(registryPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return agents.Source{}, err
		}
		var record sessionRegistry
		if err := json.Unmarshal(data, &record); err != nil {
			return agents.Source{}, fmt.Errorf("invalid Claude session registry %s: %w", registryPath, err)
		}
		if record.PID != pid {
			return agents.Source{}, fmt.Errorf("Claude registry PID disagrees with its filename: %s", registryPath)
		}
		if record.Kind != "interactive" || record.Entrypoint != "cli" || record.PIDDomain != runtime.GOOS {
			continue
		}
		if !agents.ValidID(record.SessionID) || strings.TrimSpace(record.ProcStart) == "" {
			return agents.Source{}, fmt.Errorf("Claude registry lacks a valid session ID/process start: %s", registryPath)
		}
		start, err := r.Run("ps", "-p", strconv.Itoa(pid), "-o", "lstart=")
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) && exit.ExitCode() == 1 {
				continue
			}
			return agents.Source{}, err
		}
		if normalizeStart(string(start)) != normalizeStart(record.ProcStart) {
			continue
		}
		paths, err := filepath.Glob(filepath.Join(r.Dir, "projects", "*", record.SessionID+".jsonl"))
		if err != nil {
			return agents.Source{}, err
		}
		if len(paths) == 0 {
			return agents.Source{}, fmt.Errorf("Claude session %s has no transcript file; finish a conversation turn and retry", record.SessionID)
		}
		if len(paths) != 1 {
			return agents.Source{}, fmt.Errorf("Claude session %s has %d transcript files; expected one persisted conversation", record.SessionID, len(paths))
		}
		root, err := filepath.EvalSymlinks(filepath.Join(r.Dir, "projects"))
		if err != nil {
			return agents.Source{}, err
		}
		root, err = filepath.Abs(root)
		if err != nil {
			return agents.Source{}, err
		}
		canonical, err := filepath.EvalSymlinks(paths[0])
		if err != nil {
			return agents.Source{}, err
		}
		canonical, err = filepath.Abs(canonical)
		if err != nil {
			return agents.Source{}, err
		}
		relative, err := filepath.Rel(root, canonical)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || len(strings.Split(relative, string(filepath.Separator))) != 2 || filepath.Base(canonical) != record.SessionID+".jsonl" {
			return agents.Source{}, fmt.Errorf("Claude transcript is not a canonical root session file: %s", paths[0])
		}
		info, err := os.Stat(canonical)
		if err != nil {
			return agents.Source{}, err
		}
		if !info.Mode().IsRegular() {
			return agents.Source{}, fmt.Errorf("Claude transcript is not a regular file: %s", paths[0])
		}
		records, _, err := transcript.ReadJSONL(canonical)
		if err != nil {
			return agents.Source{}, err
		}
		id, err := claudeSessionID(canonical, records)
		if err != nil {
			return agents.Source{}, err
		}
		if _, ok := Detect(records); !ok {
			return agents.Source{}, fmt.Errorf("unrecognized Claude transcript: %s", paths[0])
		}
		if id != record.SessionID {
			return agents.Source{}, fmt.Errorf("Claude registry and transcript session IDs disagree")
		}
		after, err := os.ReadFile(registryPath)
		if err != nil {
			return agents.Source{}, fmt.Errorf("Claude registry changed during discovery; retry: %w", err)
		}
		var current sessionRegistry
		if json.Unmarshal(after, &current) != nil || current.PID != record.PID || current.SessionID != record.SessionID || normalizeStart(current.ProcStart) != normalizeStart(record.ProcStart) || current.PIDDomain != record.PIDDomain || current.Kind != record.Kind || current.Entrypoint != record.Entrypoint {
			return agents.Source{}, fmt.Errorf("Claude session changed during discovery; retry")
		}
		again, err := r.Run("ps", "-p", strconv.Itoa(pid), "-o", "lstart=")
		if err != nil || normalizeStart(string(again)) != normalizeStart(record.ProcStart) {
			return agents.Source{}, fmt.Errorf("Claude process changed during discovery; retry")
		}
		matches = append(matches, agents.Source{Harness: "claude", Path: canonical})
	}
	return agents.Unique("claude", matches)
}

func normalizeStart(value string) string { return strings.Join(strings.Fields(value), " ") }

func ConfigDir() string {
	if path := os.Getenv("CLAUDE_CONFIG_DIR"); path != "" {
		return agents.ExpandHome(path)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

func Detect(records []map[string]any) (string, bool) {
	id, err := claudeSessionID("transcript", records)
	if err != nil {
		return "", false
	}
	for _, record := range records {
		kind := transcript.String(record, "type")
		if (kind == "user" || kind == "assistant") && transcript.Object(record, "message") != nil && transcript.String(record, "sessionId") == id {
			return id, true
		}
	}
	return "", false
}
