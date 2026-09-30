package gjc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeGJCIndexFixture is the shared unit-fixture contract: snapshotEvents are
// complete signed JSON event rows, tailEvents are complete signed JSONL rows.
// A missing snapshot is represented by snapshotPresent=false; an empty log is a
// present zero-byte log. This keeps missing and empty-file behavior testable.
func writeGJCIndexFixture(t *testing.T, agentDir string, snapshotPresent bool, snapshotSeq int64, snapshotEvents, tailEvents [][]byte) {
	t.Helper()
	dir := filepath.Join(agentDir, "sdk", "sessions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if snapshotPresent {
		var builder strings.Builder
		builder.WriteString(fmt.Sprintf(`{"version":4,"indexSeq":%d,"events":[`, snapshotSeq))
		for index, event := range snapshotEvents {
			if index > 0 {
				builder.WriteByte(',')
			}
			builder.Write(event)
		}
		builder.WriteString("]}")
		if err := os.WriteFile(filepath.Join(dir, "index.snapshot.json"), []byte(builder.String()), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if tailEvents != nil {
		var builder strings.Builder
		for _, event := range tailEvents {
			builder.Write(event)
			builder.WriteByte('\n')
		}
		if err := os.WriteFile(filepath.Join(dir, "index.jsonl"), []byte(builder.String()), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func gjcTestEvent(t *testing.T, version, sequence int64, sessionID, eventType string, pid, generation int, stateRoot, identity string, timestamp int64, terminalUncertain, legacy bool) []byte {
	t.Helper()
	locator := fmt.Sprintf(`{"cwd":"/workspace","worktreeRoot":null,"stateRoot":%q}`, stateRoot)
	if legacy {
		locator = fmt.Sprintf(`{"cwd":"/workspace","repo":%q}`, stateRoot)
	}
	unsigned := fmt.Sprintf(`{"version":%d,"indexSeq":%d,"type":%q,"sessionId":%q,"locator":%s,"endpointGeneration":%d,"pid":%d,"hostIncarnation":%q,"ts":%d`, version, sequence, eventType, sessionID, locator, generation, pid, identity, timestamp)
	if terminalUncertain {
		unsigned += `,"terminalUncertain":true`
	}
	return []byte(unsigned + fmt.Sprintf(`,"checksum":"%s"}`, gjcChecksum([]byte(unsigned+"}"))))
}

func TestGJCIndexMissingEmptyAndMalformedAreDistinct(t *testing.T) {
	dir := t.TempDir()
	events, err := gjcReadIndex(dir)
	if err != nil || len(events) != 0 {
		t.Fatalf("missing index files = %v, %v", events, err)
	}
	indexDir := filepath.Join(dir, "sdk", "sessions")
	if err := os.MkdirAll(indexDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(indexDir, "index.snapshot.json"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := gjcReadIndex(dir); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty snapshot accepted: %v", err)
	}
	if err := os.WriteFile(filepath.Join(indexDir, "index.snapshot.json"), []byte(`{"version":4,"indexSeq":0,"events":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(indexDir, "index.jsonl"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := gjcReadIndex(dir); err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("malformed log accepted: %v", err)
	}
}

func TestGJCIndexSnapshotAllowsSequenceGaps(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UnixMilli()
	first := gjcTestEvent(t, 4, 1, "main", "host_registered", 100, 1, filepath.Join(dir, "state", "main"), "darwin:1:2", now, false, false)
	third := gjcTestEvent(t, 4, 3, "main", "lifecycle_started", 100, 1, filepath.Join(dir, "state", "main"), "darwin:1:2", now, false, false)
	writeGJCIndexFixture(t, dir, true, 3, [][]byte{first, third}, nil)
	events, err := gjcReadIndex(dir)
	if err != nil || len(events) != 2 || events[1].indexSeq != 3 {
		t.Fatalf("snapshot gap rejected: %v (%v)", events, err)
	}
}

func TestGJCIndexRotationPrefixIsValidatedAndSkipped(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UnixMilli()
	root := filepath.Join(dir, "state", "main")
	one := gjcTestEvent(t, 4, 1, "main", "host_registered", 100, 1, root, "darwin:1:2", now, false, false)
	two := gjcTestEvent(t, 4, 2, "main", "lifecycle_started", 100, 1, root, "darwin:1:2", now, false, false)
	three := gjcTestEvent(t, 4, 3, "main", "host_heartbeat", 100, 1, root, "darwin:1:2", now, false, false)
	four := gjcTestEvent(t, 4, 4, "main", "host_heartbeat", 100, 1, root, "darwin:1:2", now, false, false)
	writeGJCIndexFixture(t, dir, true, 3, [][]byte{one, three}, [][]byte{one, two, three, four})
	events, err := gjcReadIndex(dir)
	if err != nil || len(events) != 3 || events[len(events)-1].indexSeq != 4 {
		t.Fatalf("rotation overlap replay = %v, %v", events, err)
	}
}

func TestGJCIndexRejectsNoncontiguousRotationPrefix(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UnixMilli()
	root := filepath.Join(dir, "state", "main")
	one := gjcTestEvent(t, 4, 1, "main", "host_registered", 100, 1, root, "darwin:1:2", now, false, false)
	three := gjcTestEvent(t, 4, 3, "main", "host_heartbeat", 100, 1, root, "darwin:1:2", now, false, false)
	writeGJCIndexFixture(t, dir, true, 3, [][]byte{one, three}, [][]byte{one, three})
	if _, err := gjcReadIndex(dir); err == nil || !strings.Contains(err.Error(), "contiguous") {
		t.Fatalf("noncontiguous historical prefix accepted: %v", err)
	}
}

func TestGJCIndexQuarantinesV1LocatorWithoutTranslation(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UnixMilli()
	legacy := gjcTestEvent(t, 1, 1, "legacy", "host_registered", 100, 1, filepath.Join(dir, "state", "legacy"), "darwin:1:2", now, false, true)
	writeGJCIndexFixture(t, dir, true, 1, [][]byte{legacy}, nil)
	events, err := gjcReadIndex(dir)
	if err != nil || len(events) != 1 || !events[0].legacyLocator {
		t.Fatalf("legacy row was not retained for quarantine: %v, %v", events, err)
	}
	projection := gjcReduceIndex(events, dir, time.UnixMilli(now))
	if len(projection.sessions) != 0 {
		t.Fatalf("legacy locator was selected or translated: %+v", projection.sessions)
	}
}

func TestGJCIndexChecksumPreservesFieldOrder(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UnixMilli()
	event := gjcTestEvent(t, 4, 1, "main", "host_registered", 100, 1, filepath.Join(dir, "state", "main"), "darwin:1:2", now, false, false)
	text := string(event)
	text = strings.Replace(text, `{"version":4,"indexSeq":1`, `{"indexSeq":1,"version":4`, 1)
	writeGJCIndexFixture(t, dir, false, 0, nil, [][]byte{[]byte(text)})
	if _, err := gjcReadIndex(dir); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("reordered signed event accepted: %v", err)
	}
}

func TestGJCIndexRejectsNullEventsEnvelope(t *testing.T) {
	dir := t.TempDir()
	indexDir := filepath.Join(dir, "sdk", "sessions")
	if err := os.MkdirAll(indexDir, 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"version":4,"indexSeq":0,"events":null}`)
	if err := os.WriteFile(filepath.Join(indexDir, "index.snapshot.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := gjcReadIndex(dir); err == nil || !strings.Contains(err.Error(), "expected array") {
		t.Fatalf("null events envelope accepted: %v", err)
	}
}

func TestGJCIndexRejectsUnsafeSequenceInteger(t *testing.T) {
	dir := t.TempDir()
	event := gjcTestEvent(t, 4, 9007199254740992, "main", "host_registered", 100, 1, dir, "darwin:1:2", time.Now().UnixMilli(), false, false)
	if _, err := gjcParseEvent(event, "fixture"); err == nil {
		t.Fatal("unsafe JSON indexSeq accepted")
	}
}
