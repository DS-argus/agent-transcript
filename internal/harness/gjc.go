// Package harness identifies the foreground agent before locating its session.
package harness

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agent-transcript/internal/transcript"
)

// GJCAgentDir resolves the documented GJC environment override and default.
func GJCAgentDir() string {
	if path := os.Getenv("GJC_CODING_AGENT_DIR"); path != "" {
		return expandHome(path)
	}
	config := os.Getenv("GJC_CONFIG_DIR")
	if config == "" {
		config = os.Getenv("PI_CONFIG_DIR")
	}
	if config == "" {
		config = ".gjc"
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, strings.TrimLeft(config, "/"), "agent")
}

const gjcFreshnessWindow = 120 * time.Second

type gjcSession struct {
	sessionID          string
	pid                int
	endpointGeneration int
	hostIncarnation    string
	identityProvenance string
	stateRoot          string
	live               bool
	deleted            bool
	terminalUncertain  bool
	ambiguous          bool
	directBookkeeping  bool
	indexSeq           int64
	latestType         string
	lastHeartbeatAt    int64
}

// gjcSessions performs discovery entirely from the broker's durable index. The
// transcript itself remains the source of conversation contents; the index only
// binds the current foreground process to one transcript ID.
func (r Resolver) gjcSessions(pids []int) ([]Source, error) {
	allowed := gjcAllowedPIDs(pids)
	rows, err := gjcReadIndex(r.GJCDir)
	if err != nil {
		return nil, err
	}
	identities, err := gjcProcessIdentities(r, pids)
	if err != nil {
		return nil, err
	}
	candidate, err := gjcSelectSession(rows, r.GJCDir, allowed, identities, time.Now())
	if err != nil {
		return nil, err
	}
	if candidate == nil {
		return nil, nil
	}

	path, err := gjcSessionFile(r.GJCDir, candidate.sessionID)
	if err != nil {
		return nil, err
	}
	if err := gjcValidateSessionHeader(path, candidate.sessionID); err != nil {
		return nil, err
	}

	// Fork/resume can replace the selected row without replacing its process.
	// Re-read both the index and every allowed process identity after checking
	// the transcript header. Heartbeat/indexSeq-only changes are intentionally
	// ignored by gjcSessionSame; authority changes are not.
	againRows, err := gjcReadIndex(r.GJCDir)
	if err != nil {
		return nil, fmt.Errorf("GJC session index changed during discovery; retry: %w", err)
	}
	againIdentities, err := gjcProcessIdentities(r, pids)
	if err != nil {
		return nil, fmt.Errorf("GJC process changed during discovery; retry: %w", err)
	}
	if !gjcIdentitySnapshotsEqual(identities, againIdentities) {
		return nil, fmt.Errorf("GJC process changed during discovery; retry")
	}
	againCandidate, err := gjcSelectSession(againRows, r.GJCDir, allowed, againIdentities, time.Now())
	if err != nil {
		return nil, fmt.Errorf("GJC session changed during discovery; retry: %w", err)
	}
	if againCandidate == nil || !gjcSessionSame(candidate, againCandidate) {
		return nil, fmt.Errorf("GJC session changed during discovery; retry")
	}
	return []Source{{Harness: "gjc", Path: path}}, nil
}

func gjcAllowedPIDs(pids []int) map[int]bool {
	allowed := make(map[int]bool, len(pids))
	for _, pid := range pids {
		allowed[pid] = true
	}
	return allowed
}

func gjcProcessIdentities(r Resolver, pids []int) (map[int]string, error) {
	identities := make(map[int]string, len(pids))
	seen := make(map[int]bool, len(pids))
	for _, pid := range pids {
		if seen[pid] {
			continue
		}
		seen[pid] = true
		identity, err := r.Identity(pid)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("cannot verify GJC process %d: %w", pid, err)
		}
		if identity == "" {
			return nil, fmt.Errorf("cannot verify GJC process %d: empty process identity", pid)
		}
		identities[pid] = identity
	}
	return identities, nil
}

func gjcIdentitySnapshotsEqual(left, right map[int]string) bool {
	if len(left) != len(right) {
		return false
	}
	for pid, identity := range left {
		if right[pid] != identity {
			return false
		}
	}
	return true
}

func gjcSelectSession(events []gjcIndexEvent, agentDir string, allowed map[int]bool, identities map[int]string, now time.Time) (*gjcSession, error) {
	projection := gjcReduceIndex(events, agentDir, now)

	// Keep the endpoint fence separate from public live rows. A stopped or
	// uncertain endpoint still proves that a generation-0 row is only GC
	// bookkeeping and must not be used as a fallback for this process.
	endpointByIdentity := make(map[string]bool)
	identityMismatch := false
	for _, row := range projection.identities {
		if !allowed[row.pid] {
			continue
		}
		identity, ok := identities[row.pid]
		if !ok || row.hostIncarnation != identity || row.identityProvenance != "composite" {
			identityMismatch = true
			continue
		}
		if row.endpointGeneration > 0 {
			endpointByIdentity[fmt.Sprintf("%d\x00%s", row.pid, identity)] = true
		}
	}

	candidates := make([]gjcSession, 0, len(projection.sessions))
	retiredEndpoint := make(map[int]string)
	for _, row := range projection.sessions {
		if !allowed[row.pid] {
			continue
		}
		identity, ok := identities[row.pid]
		if !ok || row.hostIncarnation != identity || row.identityProvenance != "composite" {
			identityMismatch = true
			continue
		}
		if row.ambiguous {
			return nil, fmt.Errorf("ambiguous GJC session authority for %s", row.sessionID)
		}
		if row.endpointGeneration > 0 && (!row.live || row.deleted || row.terminalUncertain) {
			state := "not live"
			if row.deleted {
				state = "deleted"
			} else if row.terminalUncertain {
				state = "terminal-uncertain"
			}
			retiredEndpoint[row.pid] = state
		}
		if !row.live || row.deleted || row.terminalUncertain {
			continue
		}
		if row.endpointGeneration == 0 && row.directBookkeeping && endpointByIdentity[fmt.Sprintf("%d\x00%s", row.pid, identity)] {
			retiredEndpoint[row.pid] = "superseded by endpoint"
			continue
		}
		candidates = append(candidates, row)
	}
	if len(candidates) > 1 {
		return nil, fmt.Errorf("ambiguous GJC sessions: %d proven live sessions", len(candidates))
	}
	if len(candidates) == 1 {
		return &candidates[0], nil
	}
	if identityMismatch {
		return nil, fmt.Errorf("GJC session process identity mismatch")
	}
	for pid, state := range retiredEndpoint {
		return nil, fmt.Errorf("GJC endpoint for PID %d is %s", pid, state)
	}
	return nil, nil
}

func gjcSessionSame(left, right *gjcSession) bool {
	return left.sessionID == right.sessionID && left.pid == right.pid &&
		left.endpointGeneration == right.endpointGeneration &&
		left.hostIncarnation == right.hostIncarnation &&
		left.identityProvenance == right.identityProvenance &&
		gjcCanonicalPath(left.stateRoot) == gjcCanonicalPath(right.stateRoot) &&
		left.live == right.live && left.deleted == right.deleted &&
		left.terminalUncertain == right.terminalUncertain && left.ambiguous == right.ambiguous &&
		left.directBookkeeping == right.directBookkeeping
}

func gjcSessionFile(agentDir, sessionID string) (string, error) {
	if !validID(sessionID) {
		return "", fmt.Errorf("invalid GJC session ID")
	}
	paths, err := filepath.Glob(filepath.Join(agentDir, "sessions", "*", "*_"+sessionID+".jsonl"))
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("GJC session %s has no transcript file; finish a conversation turn and retry", sessionID)
	}
	if len(paths) != 1 {
		return "", fmt.Errorf("GJC session %s has %d transcript files; expected one persisted conversation", sessionID, len(paths))
	}
	return paths[0], nil
}

func gjcValidateSessionHeader(path, expectedID string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	line, err := bufio.NewReader(file).ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return fmt.Errorf("GJC session %s has no valid header", expectedID)
	}
	var header struct {
		Type    string `json:"type"`
		Version *int64 `json:"version"`
		ID      string `json:"id"`
	}
	if err := json.Unmarshal(line, &header); err != nil {
		return fmt.Errorf("invalid GJC session header: %w", err)
	}
	if header.Type != "session" || header.Version == nil || *header.Version != 5 || header.ID != expectedID {
		return fmt.Errorf("GJC session index and transcript file identity disagree")
	}
	return nil
}

func gjcCanonicalPath(path string) string {
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Clean(path)
}

func recognizeGJC(records []map[string]any) (string, bool) {
	if len(records) == 0 || transcript.String(records[0], "type") != "session" {
		return "", false
	}
	return transcript.String(records[0], "id"), true
}
