package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"agent-transcript/internal/agents"
	"agent-transcript/internal/process"
	"agent-transcript/internal/transcript"
)

type Resolver struct {
	Run process.Runner
}

func (r Resolver) Locate(pids []int) (agents.Source, error) {
	if err := agents.ValidatePIDs("codex", pids); err != nil {
		return agents.Source{}, err
	}
	if r.Run == nil {
		r.Run = process.Run
	}
	matches, err := r.locateCodex(pids)
	if err != nil {
		return agents.Source{}, err
	}
	return agents.Unique("codex", matches)
}

type codexRollout struct {
	source agents.Source
	parent string
}

func (r Resolver) locateCodex(pids []int) ([]agents.Source, error) {
	paths, err := process.OpenFiles(pids, r.Run)
	if err != nil {
		return nil, err
	}
	var matches []agents.Source
	byID := make(map[string]codexRollout)
	seenPaths := make(map[string]bool)
	for _, path := range paths {
		if !strings.HasPrefix(filepath.Base(path), "rollout-") {
			continue
		}
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, err
		}
		canonical, err = filepath.Abs(canonical)
		if err != nil {
			return nil, err
		}
		if seenPaths[canonical] {
			continue
		}
		seenPaths[canonical] = true
		metadata, err := codexRolloutMetadata(path)
		if err != nil {
			return nil, err
		}
		// Source records how the session was created, not its current host.
		// App-server sessions retain "vscode" when resumed in a local TUI.
		switch transcript.String(metadata, "source") {
		case "cli", "vscode":
		default:
			continue
		}
		id := transcript.String(metadata, "id")
		if !agents.ValidID(id) {
			return nil, fmt.Errorf("invalid Codex rollout session ID: %s", path)
		}
		if _, duplicate := byID[id]; duplicate {
			return nil, fmt.Errorf("duplicate Codex rollout session ID %s", id)
		}
		parent := ""
		if value, exists := metadata["forked_from_id"]; exists && value != nil {
			var ok bool
			parent, ok = value.(string)
			if !ok || !agents.ValidID(parent) || parent == id {
				return nil, fmt.Errorf("invalid Codex fork parent for session %s", id)
			}
		}
		source := agents.Source{Harness: "codex", Path: path}
		byID[id] = codexRollout{source: source, parent: parent}
		matches = append(matches, source)
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("No local Codex transcript found. For shared-server sessions, reopen with codex --no-daemon; otherwise finish a conversation turn.")
	}
	if len(matches) < 2 {
		return matches, nil
	}

	// A fork can keep its original rollout open in the same process. Prefer a
	// unique descendant only when every owned interactive candidate lies on that one
	// parent chain. This is a fork-display policy, not access to the TUI's
	// in-memory active_thread_id; timestamps and history_base are not authority.
	parents := make(map[string]bool)
	for _, candidate := range byID {
		if _, exists := byID[candidate.parent]; exists {
			parents[candidate.parent] = true
		}
	}
	tip := ""
	for id := range byID {
		if !parents[id] {
			if tip != "" {
				return matches, nil // Siblings or unrelated threads remain ambiguous.
			}
			tip = id
		}
	}
	visited := make(map[string]bool)
	for id := tip; ; {
		candidate, exists := byID[id]
		if !exists {
			break
		}
		if visited[id] {
			return nil, fmt.Errorf("cyclic Codex fork ancestry")
		}
		visited[id] = true
		id = candidate.parent
	}
	if len(visited) != len(byID) {
		return nil, fmt.Errorf("Codex fork candidates do not form one acyclic ancestry chain")
	}
	return []agents.Source{byID[tip].source}, nil
}

// Discovery needs only the header. The transcript parser validates the selected
// file's body; a retained parent's body is neither rendered nor read here.
func codexRolloutMetadata(path string) (map[string]any, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	line, err := bufio.NewReader(file).ReadBytes('\n')
	if err != nil && err != io.EOF {
		return nil, err
	}
	var header map[string]any
	if err := json.Unmarshal(line, &header); err != nil {
		return nil, fmt.Errorf("invalid Codex rollout header %s: %w", path, err)
	}
	metadata := transcript.Object(header, "payload")
	if transcript.String(header, "type") != "session_meta" || metadata == nil {
		return nil, fmt.Errorf("invalid Codex rollout: %s", path)
	}
	return metadata, nil
}

func Detect(records []map[string]any) (string, bool) {
	if len(records) == 0 || transcript.String(records[0], "type") != "session_meta" {
		return "", false
	}
	return transcript.String(transcript.Object(records[0], "payload"), "id"), true
}
