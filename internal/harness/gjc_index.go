package harness

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	gjcIndexSnapshotVersion = 4
	gjcIndexReadAttempts    = 3
)

type gjcIndexLocator struct {
	cwd          string
	worktreeRoot string
	stateRoot    string
}

type gjcIndexEvent struct {
	version            int64
	indexSeq           int64
	eventType          string
	sessionID          string
	locator            gjcIndexLocator
	legacyLocator      bool
	endpointGeneration int
	pid                int
	processIncarnation string
	hostIncarnation    string
	identitySet        bool
	terminalUncertain  bool
	forcedStaleRelease bool
	ts                 int64
	checksum           string
}

type gjcIndexProjection struct {
	identities []gjcSession
	sessions   []gjcSession
}

type gjcIdentityState struct {
	identity  string
	latest    gjcIndexEvent
	heartbeat *gjcIndexEvent
}

// gjcReadIndex reads a stable snapshot/journal pair. It intentionally does
// not use an encoding/json map for checksum verification: the broker signs
// JSON.stringify's insertion order, while map re-marshalling sorts keys.
func gjcReadIndex(agentDir string) ([]gjcIndexEvent, error) {
	snapshotPath := filepath.Join(agentDir, "sdk", "sessions", "index.snapshot.json")
	logPath := filepath.Join(agentDir, "sdk", "sessions", "index.jsonl")
	for attempt := 0; attempt < gjcIndexReadAttempts; attempt++ {
		beforeSnapshot, err := statGJCIndexFile(snapshotPath)
		if err != nil {
			return nil, err
		}
		beforeLog, err := statGJCIndexFile(logPath)
		if err != nil {
			return nil, err
		}
		snapshot, snapshotExists, err := gjcReadOptional(snapshotPath)
		if err != nil {
			return nil, err
		}
		log, logExists, err := gjcReadOptional(logPath)
		if err != nil {
			return nil, err
		}

		var events []gjcIndexEvent
		var parseErr error
		var snapshotSeq int64
		if snapshotExists {
			var snapshotEvents []gjcIndexEvent
			snapshotEvents, snapshotSeq, parseErr = gjcParseSnapshot(snapshot, snapshotPath)
			events = snapshotEvents
		}
		if parseErr == nil && logExists {
			var tail []gjcIndexEvent
			tail, parseErr = gjcParseTail(log, snapshotSeq, logPath)
			events = append(events, tail...)
		}

		afterSnapshot, stampErr := statGJCIndexFile(snapshotPath)
		if stampErr != nil {
			return nil, stampErr
		}
		afterLog, stampErr := statGJCIndexFile(logPath)
		if stampErr != nil {
			return nil, stampErr
		}
		if !beforeSnapshot.equal(afterSnapshot) || !beforeLog.equal(afterLog) {
			if attempt+1 < gjcIndexReadAttempts {
				time.Sleep(time.Millisecond)
			}
			continue
		}
		if parseErr != nil {
			return nil, parseErr
		}
		return events, nil
	}
	return nil, fmt.Errorf("GJC session index changed during discovery; retry")
}

type gjcIndexFileStamp struct {
	exists bool
	size   int64
	file   os.FileInfo
	mtime  int64
}

func statGJCIndexFile(path string) (gjcIndexFileStamp, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return gjcIndexFileStamp{}, nil
	}
	if err != nil {
		return gjcIndexFileStamp{}, err
	}
	return gjcIndexFileStamp{exists: true, size: info.Size(), file: info, mtime: info.ModTime().UnixNano()}, nil
}

func (stamp gjcIndexFileStamp) equal(other gjcIndexFileStamp) bool {
	return stamp.exists == other.exists && stamp.size == other.size && stamp.mtime == other.mtime &&
		(!stamp.exists || os.SameFile(stamp.file, other.file))
}
func gjcReadOptional(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

type gjcJSONField struct {
	name string
	raw  []byte
}

func gjcOrderedObject(data []byte) ([]gjcJSONField, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if token != json.Delim('{') {
		return nil, fmt.Errorf("expected JSON object")
	}
	fields := make([]gjcJSONField, 0, 16)
	seen := make(map[string]bool)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok || seen[key] {
			return nil, fmt.Errorf("invalid or duplicate JSON object key")
		}
		seen[key] = true
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, err
		}
		compact, err := gjcCompactJSON(raw)
		if err != nil {
			return nil, err
		}
		fields = append(fields, gjcJSONField{name: key, raw: compact})
	}
	end, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if end != json.Delim('}') {
		return nil, fmt.Errorf("unterminated JSON object")
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("trailing JSON value")
		}
		return nil, err
	}
	return fields, nil
}

func gjcCompactJSON(raw []byte) ([]byte, error) {
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return nil, err
	}
	return compact.Bytes(), nil
}

func gjcJSONFieldValue(fields []gjcJSONField, name string) ([]byte, bool) {
	for _, field := range fields {
		if field.name == name {
			return field.raw, true
		}
	}
	return nil, false
}

func gjcJSONString(value string) []byte {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
	return bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'})
}

func gjcUnsignedJSON(fields []gjcJSONField) ([]byte, error) {
	var buffer bytes.Buffer
	buffer.WriteByte('{')
	first := true
	for _, field := range fields {
		if field.name == "checksum" {
			continue
		}
		if !first {
			buffer.WriteByte(',')
		}
		first = false
		buffer.Write(gjcJSONString(field.name))
		buffer.WriteByte(':')
		buffer.Write(field.raw)
	}
	buffer.WriteByte('}')
	return buffer.Bytes(), nil
}

func gjcChecksum(unsigned []byte) string {
	sum := sha256.Sum256(unsigned)
	return hex.EncodeToString(sum[:])
}

func gjcParseSnapshot(data []byte, source string) ([]gjcIndexEvent, int64, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, 0, fmt.Errorf("empty GJC session index snapshot: %s", source)
	}
	fields, err := gjcOrderedObject(data)
	if err != nil {
		return nil, 0, fmt.Errorf("malformed GJC session index snapshot %s: %w", source, err)
	}
	versionRaw, ok := gjcJSONFieldValue(fields, "version")
	if !ok {
		return nil, 0, fmt.Errorf("GJC session index snapshot %s is missing version", source)
	}
	version, err := gjcJSONInt(versionRaw)
	if err != nil {
		return nil, 0, fmt.Errorf("malformed GJC session index snapshot %s version: %w", source, err)
	}
	if version > gjcIndexSnapshotVersion {
		return nil, 0, fmt.Errorf("unsupported GJC session index snapshot version %d", version)
	}
	if version != gjcIndexSnapshotVersion {
		return nil, 0, fmt.Errorf("unsupported GJC session index snapshot version %d", version)
	}
	seqRaw, ok := gjcJSONFieldValue(fields, "indexSeq")
	if !ok {
		return nil, 0, fmt.Errorf("GJC session index snapshot %s is missing indexSeq", source)
	}
	snapshotSeq, err := gjcJSONInt(seqRaw)
	if err != nil || snapshotSeq < 0 {
		return nil, 0, fmt.Errorf("malformed GJC session index snapshot %s indexSeq", source)
	}
	eventsRaw, ok := gjcJSONFieldValue(fields, "events")
	if !ok {
		return nil, 0, fmt.Errorf("GJC session index snapshot %s is missing events", source)
	}
	var rawEvents []json.RawMessage
	if err := json.Unmarshal(eventsRaw, &rawEvents); err != nil {
		return nil, 0, fmt.Errorf("malformed GJC session index snapshot %s events: %w", source, err)
	}
	if bytes.Equal(bytes.TrimSpace(eventsRaw), []byte("null")) {
		return nil, 0, fmt.Errorf("malformed GJC session index snapshot %s events: expected array", source)
	}
	if len(rawEvents) == 0 {
		if snapshotSeq != 0 {
			return nil, 0, fmt.Errorf("GJC session index snapshot %s has empty events with nonzero indexSeq", source)
		}
		return nil, 0, nil
	}
	previous := int64(0)
	events := make([]gjcIndexEvent, 0, len(rawEvents))
	for index, raw := range rawEvents {
		event, err := gjcParseEvent(raw, source)
		if err != nil {
			return nil, 0, fmt.Errorf("invalid GJC snapshot event %d: %w", index, err)
		}
		if event.indexSeq <= previous {
			return nil, 0, fmt.Errorf("GJC snapshot event sequence is not increasing")
		}
		previous = event.indexSeq
		events = append(events, event)
	}
	if previous != snapshotSeq {
		return nil, 0, fmt.Errorf("GJC snapshot last sequence %d does not equal indexSeq %d", previous, snapshotSeq)
	}
	return events, snapshotSeq, nil
}

func gjcParseTail(data []byte, snapshotSeq int64, source string) ([]gjcIndexEvent, error) {
	if len(data) == 0 {
		return nil, nil
	}
	if data[len(data)-1] != '\n' {
		return nil, fmt.Errorf("malformed GJC session index log %s: unterminated entry", source)
	}
	lines := bytes.Split(data[:len(data)-1], []byte{'\n'})
	result := make([]gjcIndexEvent, 0, len(lines))
	var historicalLast int64
	tailStarted := false
	expected := snapshotSeq + 1
	for index, line := range lines {
		if len(line) == 0 {
			continue
		}
		event, err := gjcParseEvent(line, source)
		if err != nil {
			return nil, fmt.Errorf("invalid GJC log event %d: %w", index, err)
		}
		if !tailStarted && event.indexSeq <= snapshotSeq {
			if historicalLast != 0 && event.indexSeq != historicalLast+1 {
				return nil, fmt.Errorf("GJC historical log prefix is not contiguous")
			}
			historicalLast = event.indexSeq
			continue
		}
		if !tailStarted {
			if historicalLast != 0 && historicalLast != snapshotSeq {
				return nil, fmt.Errorf("GJC historical log prefix does not end at snapshot sequence")
			}
			tailStarted = true
		}
		if event.indexSeq != expected {
			return nil, fmt.Errorf("GJC session index log sequence %d expected %d", event.indexSeq, expected)
		}
		result = append(result, event)
		expected++
	}
	if historicalLast != 0 && !tailStarted && historicalLast != snapshotSeq {
		return nil, fmt.Errorf("GJC historical log prefix does not end at snapshot sequence")
	}
	return result, nil
}

func gjcParseEvent(data []byte, source string) (gjcIndexEvent, error) {
	fields, err := gjcOrderedObject(data)
	if err != nil {
		return gjcIndexEvent{}, fmt.Errorf("malformed event in %s: %w", source, err)
	}
	unsigned, err := gjcUnsignedJSON(fields)
	if err != nil {
		return gjcIndexEvent{}, err
	}
	checksumRaw, ok := gjcJSONFieldValue(fields, "checksum")
	if !ok {
		return gjcIndexEvent{}, fmt.Errorf("missing checksum")
	}
	var checksum string
	if err := json.Unmarshal(checksumRaw, &checksum); err != nil || checksum == "" {
		return gjcIndexEvent{}, fmt.Errorf("invalid checksum")
	}
	if checksum != gjcChecksum(unsigned) {
		return gjcIndexEvent{}, fmt.Errorf("checksum mismatch")
	}
	versionRaw, ok := gjcJSONFieldValue(fields, "version")
	if !ok {
		return gjcIndexEvent{}, fmt.Errorf("missing event version")
	}
	version, err := gjcJSONInt(versionRaw)
	if err != nil {
		return gjcIndexEvent{}, fmt.Errorf("invalid event version")
	}
	if version > gjcIndexSnapshotVersion || version != 1 && version != gjcIndexSnapshotVersion {
		return gjcIndexEvent{}, fmt.Errorf("unsupported GJC session index event version %d", version)
	}
	seqRaw, ok := gjcJSONFieldValue(fields, "indexSeq")
	if !ok {
		return gjcIndexEvent{}, fmt.Errorf("missing indexSeq")
	}
	seq, err := gjcJSONInt(seqRaw)
	if err != nil || seq <= 0 {
		return gjcIndexEvent{}, fmt.Errorf("invalid indexSeq")
	}
	typeRaw, ok := gjcJSONFieldValue(fields, "type")
	if !ok {
		return gjcIndexEvent{}, fmt.Errorf("missing event type")
	}
	var eventType string
	if err := json.Unmarshal(typeRaw, &eventType); err != nil || !gjcEventTypes[eventType] {
		return gjcIndexEvent{}, fmt.Errorf("invalid event type")
	}
	sessionRaw, ok := gjcJSONFieldValue(fields, "sessionId")
	if !ok {
		return gjcIndexEvent{}, fmt.Errorf("missing sessionId")
	}
	var sessionID string
	if err := json.Unmarshal(sessionRaw, &sessionID); err != nil || !validID(sessionID) {
		return gjcIndexEvent{}, fmt.Errorf("invalid sessionId")
	}
	pidRaw, ok := gjcJSONFieldValue(fields, "pid")
	if !ok {
		return gjcIndexEvent{}, fmt.Errorf("missing pid")
	}
	pid64, err := gjcJSONInt(pidRaw)
	if err != nil || pid64 <= 0 || pid64 > int64(^uint(0)>>1) {
		return gjcIndexEvent{}, fmt.Errorf("invalid pid")
	}
	generationRaw, ok := gjcJSONFieldValue(fields, "endpointGeneration")
	if !ok {
		return gjcIndexEvent{}, fmt.Errorf("missing endpointGeneration")
	}
	generation64, err := gjcJSONInt(generationRaw)
	if err != nil || generation64 < 0 || generation64 > int64(^uint(0)>>1) {
		return gjcIndexEvent{}, fmt.Errorf("invalid endpointGeneration")
	}
	tsRaw, ok := gjcJSONFieldValue(fields, "ts")
	if !ok {
		return gjcIndexEvent{}, fmt.Errorf("missing ts")
	}
	ts, err := gjcJSONInt(tsRaw)
	if err != nil || ts < 0 {
		return gjcIndexEvent{}, fmt.Errorf("invalid ts")
	}
	locatorRaw, ok := gjcJSONFieldValue(fields, "locator")
	if !ok {
		return gjcIndexEvent{}, fmt.Errorf("missing locator")
	}
	locator, legacyLocator, err := gjcParseLocator(locatorRaw)
	if err != nil {
		return gjcIndexEvent{}, err
	}
	processIncarnation, processSet, err := gjcOptionalString(fields, "processIncarnation")
	if err != nil {
		return gjcIndexEvent{}, err
	}
	hostIncarnation, hostSet, err := gjcOptionalString(fields, "hostIncarnation")
	if err != nil {
		return gjcIndexEvent{}, err
	}
	identity := hostIncarnation
	identitySet := hostSet
	if !identitySet {
		identity = processIncarnation
		identitySet = processSet
	}
	terminalUncertain, err := gjcOptionalBool(fields, "terminalUncertain")
	if err != nil {
		return gjcIndexEvent{}, err
	}
	forcedStaleRelease, err := gjcOptionalBool(fields, "forcedStaleRelease")
	if err != nil {
		return gjcIndexEvent{}, err
	}
	return gjcIndexEvent{
		version:            version,
		indexSeq:           seq,
		eventType:          eventType,
		sessionID:          sessionID,
		locator:            locator,
		legacyLocator:      legacyLocator || version == 1,
		endpointGeneration: int(generation64),
		pid:                int(pid64),
		processIncarnation: processIncarnation,
		hostIncarnation:    identity,
		identitySet:        identitySet,
		terminalUncertain:  terminalUncertain,
		forcedStaleRelease: forcedStaleRelease,
		ts:                 ts,
		checksum:           checksum,
	}, nil
}

var gjcEventTypes = map[string]bool{
	"host_registered":    true,
	"host_heartbeat":     true,
	"host_unregistered":  true,
	"lifecycle_started":  true,
	"lifecycle_terminal": true,
	"session_closed":     true,
	"session_deleted":    true,
	"record_reconciled":  true,
}

func gjcParseLocator(raw []byte) (gjcIndexLocator, bool, error) {
	fields, err := gjcOrderedObject(raw)
	if err != nil {
		return gjcIndexLocator{}, false, fmt.Errorf("invalid locator: %w", err)
	}
	cwdRaw, cwdOK := gjcJSONFieldValue(fields, "cwd")
	rootRaw, rootOK := gjcJSONFieldValue(fields, "stateRoot")
	worktreeRaw, worktreeOK := gjcJSONFieldValue(fields, "worktreeRoot")
	if !cwdOK || !rootOK || !worktreeOK {
		return gjcIndexLocator{}, true, nil
	}
	var cwd, stateRoot string
	if json.Unmarshal(cwdRaw, &cwd) != nil || cwd == "" || json.Unmarshal(rootRaw, &stateRoot) != nil || stateRoot == "" {
		return gjcIndexLocator{}, true, nil
	}
	worktreeRoot := ""
	if string(worktreeRaw) != "null" {
		if json.Unmarshal(worktreeRaw, &worktreeRoot) != nil {
			return gjcIndexLocator{}, true, nil
		}
	}
	if len(fields) != 3 {
		return gjcIndexLocator{}, true, nil
	}
	for _, field := range fields {
		if field.name != "cwd" && field.name != "worktreeRoot" && field.name != "stateRoot" {
			return gjcIndexLocator{}, true, nil
		}
	}
	return gjcIndexLocator{cwd: cwd, worktreeRoot: worktreeRoot, stateRoot: stateRoot}, false, nil
}

func gjcOptionalString(fields []gjcJSONField, name string) (string, bool, error) {
	raw, ok := gjcJSONFieldValue(fields, name)
	if !ok {
		return "", false, nil
	}
	if string(raw) == "null" {
		return "", false, fmt.Errorf("invalid null %s", name)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || value == "" {
		return "", false, fmt.Errorf("invalid %s", name)
	}
	return value, true, nil
}

func gjcOptionalBool(fields []gjcJSONField, name string) (bool, error) {
	raw, ok := gjcJSONFieldValue(fields, name)
	if !ok {
		return false, nil
	}
	if string(raw) == "null" {
		return false, fmt.Errorf("invalid null %s", name)
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, fmt.Errorf("invalid %s", name)
	}
	return value, nil
}

func gjcJSONInt(raw []byte) (int64, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return 0, err
	}
	number, ok := value.(json.Number)
	if !ok {
		return 0, fmt.Errorf("not an integer")
	}
	parsed, err := strconv.ParseInt(string(number), 10, 64)
	if err != nil {
		return 0, err
	}
	if parsed < -9007199254740991 || parsed > 9007199254740991 {
		return 0, fmt.Errorf("integer exceeds JSON safe range")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return 0, fmt.Errorf("trailing value")
	}
	return parsed, nil
}

func gjcAdmitEvents(events []gjcIndexEvent) []gjcIndexEvent {
	authoritative := make(map[gjcTuple]gjcIndexEvent)
	for _, event := range events {
		if event.legacyLocator || event.eventType != "host_registered" {
			continue
		}
		key := gjcTuple{sessionID: event.sessionID, generation: event.endpointGeneration, stateRoot: event.locator.stateRoot}
		current, ok := authoritative[key]
		if !ok || event.indexSeq > current.indexSeq {
			authoritative[key] = event
		}
	}
	admitted := make([]gjcIndexEvent, 0, len(events))
	for _, event := range events {
		if event.legacyLocator {
			continue
		}
		authority, ok := authoritative[gjcTuple{sessionID: event.sessionID, generation: event.endpointGeneration, stateRoot: event.locator.stateRoot}]
		if ok && authority.identitySet && event.identitySet && authority.hostIncarnation != event.hostIncarnation {
			continue
		}
		admitted = append(admitted, event)
	}
	tombstones := make(map[string]int64)
	for _, event := range admitted {
		if event.eventType != "session_deleted" {
			continue
		}
		if previous, ok := tombstones[event.sessionID]; !ok || event.indexSeq > previous {
			tombstones[event.sessionID] = event.indexSeq
		}
	}
	anchors := make(map[string]int64)
	postTombstone := make([]gjcIndexEvent, 0, len(admitted))
	for _, event := range admitted {
		key := gjcEventIdentityKey(event)
		if event.eventType == "host_registered" {
			anchors[key] = event.indexSeq
		}
		tombstone, deleted := tombstones[event.sessionID]
		if !deleted || event.indexSeq <= tombstone || event.eventType == "host_registered" {
			postTombstone = append(postTombstone, event)
			continue
		}
		anchor, anchored := anchors[key]
		if anchored && anchor > tombstone {
			postTombstone = append(postTombstone, event)
		}
	}
	return postTombstone
}

type gjcTuple struct {
	sessionID  string
	generation int
	stateRoot  string
}

func gjcEventIdentityKey(event gjcIndexEvent) string {
	return fmt.Sprintf("%s\x00%d\x00%s\x00%s", event.sessionID, event.endpointGeneration, event.locator.stateRoot, event.hostIncarnation)
}

func gjcPreferredIdentity(states []gjcIdentityState) (gjcIdentityState, bool) {
	if len(states) == 0 {
		return gjcIdentityState{}, false
	}
	preferred := states[0]
	for _, state := range states[1:] {
		if state.latest.endpointGeneration > preferred.latest.endpointGeneration ||
			(state.latest.endpointGeneration == preferred.latest.endpointGeneration && state.latest.indexSeq > preferred.latest.indexSeq) {
			preferred = state
		}
	}
	return preferred, true
}

func gjcTerminalEvent(eventType string) bool {
	return eventType == "host_unregistered" || eventType == "session_closed" || eventType == "session_deleted"
}

func gjcReduceIndex(events []gjcIndexEvent, agentDir string, now time.Time) gjcIndexProjection {
	admitted := gjcAdmitEvents(events)
	statesBySession := make(map[string][]gjcIdentityState)
	latestLifecycle := make(map[string]gjcIndexEvent)
	latestHeartbeat := make(map[string]gjcIndexEvent)
	for _, event := range admitted {
		key := gjcEventIdentityKey(event)
		if event.eventType == "host_heartbeat" {
			current, exists := latestHeartbeat[key]
			if !exists || event.indexSeq > current.indexSeq {
				latestHeartbeat[key] = event
			}
			continue
		}
		current, exists := latestLifecycle[key]
		if !exists || event.indexSeq > current.indexSeq {
			latestLifecycle[key] = event
		}
	}
	latest := make(map[string]gjcIdentityState, len(latestLifecycle))
	for key, lifecycle := range latestLifecycle {
		state := gjcIdentityState{identity: key, latest: lifecycle}
		if heartbeat, ok := latestHeartbeat[key]; ok {
			copy := heartbeat
			state.heartbeat = &copy
		}
		latest[key] = state
	}
	rootsBySession := make(map[string]map[string][]gjcIdentityState)
	for _, state := range latest {
		statesBySession[state.latest.sessionID] = append(statesBySession[state.latest.sessionID], state)
		roots := rootsBySession[state.latest.sessionID]
		if roots == nil {
			roots = make(map[string][]gjcIdentityState)
			rootsBySession[state.latest.sessionID] = roots
		}
		roots[state.latest.locator.stateRoot] = append(roots[state.latest.locator.stateRoot], state)
	}
	projection := gjcIndexProjection{}
	for sessionID, states := range statesBySession {
		roots := rootsBySession[sessionID]
		unresolved := make(map[string][]gjcIdentityState)
		fencing := make(map[string][]gjcIdentityState)
		for root, rootStates := range roots {
			preferred, ok := gjcPreferredIdentity(rootStates)
			if !ok || gjcTerminalEvent(preferred.latest.eventType) {
				continue
			}
			unresolved[root] = rootStates
			if !gjcDirectGCRow(preferred.latest, agentDir) {
				fencing[root] = rootStates
			}
		}
		ambiguous := len(fencing) > 1
		for _, state := range states {
			projection.identities = append(projection.identities, gjcProjectIdentity(state, ambiguous, agentDir, now))
		}
		preferred, ok := gjcPreferredIdentity(states)
		if !ok || preferred.latest.eventType == "session_deleted" {
			continue
		}
		authority := preferred
		var surviving map[string][]gjcIdentityState
		if len(fencing) == 1 {
			surviving = fencing
		} else if len(fencing) == 0 && len(unresolved) == 1 {
			surviving = unresolved
		}
		if len(surviving) == 1 {
			for _, rootStates := range surviving {
				if rootPreferred, ok := gjcPreferredIdentity(rootStates); ok {
					authority = rootPreferred
				}
			}
		}
		projection.sessions = append(projection.sessions, gjcProjectIdentity(authority, ambiguous, agentDir, now))
	}
	return projection
}

func gjcDirectGCRow(event gjcIndexEvent, agentDir string) bool {
	return event.eventType == "host_registered" && event.endpointGeneration == 0 &&
		gjcCanonicalPath(event.locator.stateRoot) == gjcCanonicalPath(agentDir)
}

func gjcProjectIdentity(state gjcIdentityState, ambiguous bool, agentDir string, now time.Time) gjcSession {
	latest := state.latest
	evidenceTS := int64(0)
	if state.heartbeat != nil {
		evidenceTS = state.heartbeat.ts
	}
	if latest.eventType == "host_registered" && latest.ts > evidenceTS {
		evidenceTS = latest.ts
	}
	fresh := evidenceTS > 0 && now.UnixMilli()-evidenceTS < gjcFreshnessWindow.Milliseconds()
	terminal := gjcTerminalEvent(latest.eventType)
	terminalUncertain := latest.eventType == "lifecycle_terminal" || latest.terminalUncertain
	identityProvenance := "legacy"
	if latest.identitySet {
		identityProvenance = "composite"
	}
	lastHeartbeatAt := int64(0)
	if state.heartbeat != nil {
		lastHeartbeatAt = state.heartbeat.ts
	}
	return gjcSession{
		sessionID:          latest.sessionID,
		pid:                latest.pid,
		endpointGeneration: latest.endpointGeneration,
		hostIncarnation:    latest.hostIncarnation,
		identityProvenance: identityProvenance,
		stateRoot:          latest.locator.stateRoot,
		live:               !ambiguous && !terminal && !terminalUncertain && fresh && latest.identitySet,
		deleted:            latest.eventType == "session_deleted",
		terminalUncertain:  terminalUncertain,
		ambiguous:          ambiguous,
		directBookkeeping:  gjcDirectGCRow(latest, agentDir),
		indexSeq:           latest.indexSeq,
		latestType:         latest.eventType,
		lastHeartbeatAt:    lastHeartbeatAt,
	}
}
