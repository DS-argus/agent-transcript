package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGJCOfflineDiscoverySelectsForkAndIgnoresGCRegistration(t *testing.T) {
	for _, oldFile := range []bool{false, true} {
		t.Run(fmt.Sprintf("old-file-%v", oldFile), func(t *testing.T) {
			dir := t.TempDir()
			now := time.Now().UnixMilli()
			events := [][]byte{
				gjcTestEvent(t, 4, 1, "initial", "host_registered", 100, 0, dir, "darwin:1:2", now, false, false),
				gjcTestEvent(t, 4, 2, "fork", "host_registered", 100, 1, filepath.Join(dir, "state", "fork"), "darwin:1:2", now, false, false),
			}
			writeGJCIndexFixture(t, dir, true, 0, nil, events)
			want := writeGJCHeader(t, dir, "fork")
			if oldFile {
				writeGJCHeader(t, dir, "initial")
			}
			got, err := offlineGJCResolver(dir, "darwin:1:2").Locate("gjc", []int{100})
			if err != nil || got != (Source{"gjc", want}) {
				t.Fatalf("selected source = %+v, err = %v", got, err)
			}
		})
	}
}

func TestGJCOfflineDirectOnlyRegistration(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UnixMilli()
	event := gjcTestEvent(t, 4, 1, "direct", "host_registered", 100, 0, dir, "darwin:1:2", now, false, false)
	writeGJCIndexFixture(t, dir, false, 0, nil, [][]byte{event})
	want := writeGJCHeader(t, dir, "direct")
	got, err := offlineGJCResolver(dir, "darwin:1:2").Locate("gjc", []int{100})
	if err != nil || got != (Source{"gjc", want}) {
		t.Fatalf("direct-only source = %+v, err = %v", got, err)
	}
}

func TestGJCOfflineDoesNotInvokeSDK(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UnixMilli()
	event := gjcTestEvent(t, 4, 1, "main", "host_registered", 100, 1, filepath.Join(dir, "state", "main"), "darwin:1:2", now, false, false)
	writeGJCIndexFixture(t, dir, false, 0, nil, [][]byte{event})
	want := writeGJCHeader(t, dir, "main")
	r := offlineGJCResolver(dir, "darwin:1:2")
	r.Run = func(string, ...string) ([]byte, error) {
		t.Fatal("GJC discovery invoked an external command")
		return nil, nil
	}
	got, err := r.Locate("gjc", []int{100})
	if err != nil || got.Path != want {
		t.Fatalf("offline source = %+v, err = %v", got, err)
	}
}

func TestGJCOfflineHeaderOnlyValidation(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UnixMilli()
	event := gjcTestEvent(t, 4, 1, "main", "host_registered", 100, 1, filepath.Join(dir, "state", "main"), "darwin:1:2", now, false, false)
	writeGJCIndexFixture(t, dir, false, 0, nil, [][]byte{event})
	path := writeGJCHeader(t, dir, "main")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString("not-jsonl\n")
	_ = file.Close()
	got, err := offlineGJCResolver(dir, "darwin:1:2").Locate("gjc", []int{100})
	if err != nil || got.Path != path {
		t.Fatalf("header-only discovery failed: %+v %v", got, err)
	}
}

func TestGJCOfflineRejectsStalePIDIdentity(t *testing.T) {
	dir := t.TempDir()
	event := gjcTestEvent(t, 4, 1, "main", "host_registered", 100, 1, filepath.Join(dir, "state", "main"), "darwin:old", time.Now().UnixMilli(), false, false)
	writeGJCIndexFixture(t, dir, false, 0, nil, [][]byte{event})
	if _, err := offlineGJCResolver(dir, "darwin:new").Locate("gjc", []int{100}); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("stale PID identity was accepted: %v", err)
	}
}

func TestGJCOfflineSeparatesHeartbeatFromTerminalLifecycle(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UnixMilli()
	root := filepath.Join(dir, "state", "main")
	events := [][]byte{
		gjcTestEvent(t, 4, 1, "main", "host_registered", 100, 1, root, "darwin:1:2", now-1000, false, false),
		gjcTestEvent(t, 4, 2, "main", "host_unregistered", 100, 1, root, "darwin:1:2", now-900, false, false),
		gjcTestEvent(t, 4, 3, "main", "host_heartbeat", 100, 1, root, "darwin:1:2", now, false, false),
	}
	writeGJCIndexFixture(t, dir, false, 0, nil, events)
	if _, err := offlineGJCResolver(dir, "darwin:1:2").Locate("gjc", []int{100}); err == nil || !strings.Contains(err.Error(), "endpoint") {
		t.Fatalf("stale heartbeat resurrected terminal identity: %v", err)
	}
}

func TestGJCOfflineAmbiguousRootsFailClosed(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UnixMilli()
	events := [][]byte{
		gjcTestEvent(t, 4, 1, "main", "host_registered", 100, 1, filepath.Join(dir, "state", "a"), "darwin:1:2", now, false, false),
		gjcTestEvent(t, 4, 2, "main", "host_registered", 100, 1, filepath.Join(dir, "state", "b"), "darwin:1:2", now, false, false),
	}
	writeGJCIndexFixture(t, dir, false, 0, nil, events)
	if _, err := offlineGJCResolver(dir, "darwin:1:2").Locate("gjc", []int{100}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous roots were accepted: %v", err)
	}
}

func TestGJCOfflineTerminalRootDoesNotBeatLiveRoot(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UnixMilli()
	terminalRoot := filepath.Join(dir, "state", "terminal")
	liveRoot := filepath.Join(dir, "state", "live")
	events := [][]byte{
		gjcTestEvent(t, 4, 1, "main", "host_registered", 100, 2, terminalRoot, "darwin:1:2", now, false, false),
		gjcTestEvent(t, 4, 2, "main", "host_unregistered", 100, 2, terminalRoot, "darwin:1:2", now, false, false),
		gjcTestEvent(t, 4, 3, "main", "host_registered", 100, 1, liveRoot, "darwin:1:2", now, false, false),
	}
	writeGJCIndexFixture(t, dir, false, 0, nil, events)
	want := writeGJCHeader(t, dir, "main")
	got, err := offlineGJCResolver(dir, "darwin:1:2").Locate("gjc", []int{100})
	if err != nil || got.Path != want {
		t.Fatalf("live root was not selected: %+v %v", got, err)
	}
}

func TestGJCOfflinePreferredDeletionSuppressesOtherRoots(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UnixMilli()
	deletedRoot := filepath.Join(dir, "state", "deleted")
	liveRoot := filepath.Join(dir, "state", "live")
	events := [][]byte{
		gjcTestEvent(t, 4, 1, "main", "host_registered", 100, 2, deletedRoot, "darwin:1:2", now, false, false),
		gjcTestEvent(t, 4, 2, "main", "session_deleted", 100, 2, deletedRoot, "darwin:1:2", now, false, false),
		gjcTestEvent(t, 4, 3, "main", "host_registered", 100, 1, liveRoot, "darwin:1:2", now, false, false),
	}
	writeGJCIndexFixture(t, dir, false, 0, nil, events)
	if _, err := offlineGJCResolver(dir, "darwin:1:2").Locate("gjc", []int{100}); err == nil {
		t.Fatal("preferred deleted session root was allowed to resurrect")
	}
}

func TestGJCOfflineRetiredEndpointFencesDirectFallback(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UnixMilli()
	direct := gjcTestEvent(t, 4, 1, "direct", "host_registered", 100, 0, dir, "darwin:1:2", now, false, false)
	endpointRoot := filepath.Join(dir, "state", "endpoint")
	endpoint := gjcTestEvent(t, 4, 2, "endpoint", "host_registered", 100, 1, endpointRoot, "darwin:1:2", now, false, false)
	retired := gjcTestEvent(t, 4, 3, "endpoint", "host_unregistered", 100, 1, endpointRoot, "darwin:1:2", now, false, false)
	writeGJCIndexFixture(t, dir, false, 0, nil, [][]byte{direct, endpoint, retired})
	writeGJCHeader(t, dir, "direct")
	if _, err := offlineGJCResolver(dir, "darwin:1:2").Locate("gjc", []int{100}); err == nil || !strings.Contains(err.Error(), "endpoint") {
		t.Fatalf("retired endpoint allowed direct fallback: %v", err)
	}
}

func TestGJCOfflineRecheckAllowsHeartbeatOnlyChange(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UnixMilli()
	root := filepath.Join(dir, "state", "main")
	events := [][]byte{
		gjcTestEvent(t, 4, 1, "main", "host_registered", 100, 1, root, "darwin:1:2", now, false, false),
		gjcTestEvent(t, 4, 2, "main", "host_heartbeat", 100, 1, root, "darwin:1:2", now, false, false),
	}
	writeGJCIndexFixture(t, dir, false, 0, nil, events)
	want := writeGJCHeader(t, dir, "main")
	r := offlineGJCResolver(dir, "darwin:1:2")
	calls := 0
	r.Identity = func(int) (string, error) {
		calls++
		if calls == 1 {
			heartbeat := gjcTestEvent(t, 4, 3, "main", "host_heartbeat", 100, 1, root, "darwin:1:2", now+1, false, false)
			writeGJCIndexFixture(t, dir, false, 0, nil, append(events, heartbeat))
		}
		return "darwin:1:2", nil
	}
	got, err := r.Locate("gjc", []int{100})
	if err != nil || got.Path != want {
		t.Fatalf("heartbeat-only recheck changed authority: %+v %v", got, err)
	}
}

func TestGJCOfflineRecheckRejectsSamePIDSessionSwitch(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UnixMilli()
	oldRoot := filepath.Join(dir, "state", "old")
	newRoot := filepath.Join(dir, "state", "new")
	oldEvent := gjcTestEvent(t, 4, 1, "old", "host_registered", 100, 1, oldRoot, "darwin:1:2", now, false, false)
	newEvent := gjcTestEvent(t, 4, 2, "new", "host_registered", 100, 1, newRoot, "darwin:1:2", now, false, false)
	writeGJCIndexFixture(t, dir, false, 0, nil, [][]byte{oldEvent})
	writeGJCHeader(t, dir, "old")
	calls := 0
	r := Resolver{
		GJCDir: dir,
		Identity: func(int) (string, error) {
			calls++
			if calls == 1 {
				file, err := os.OpenFile(filepath.Join(dir, "sdk", "sessions", "index.jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
				if err != nil {
					return "", err
				}
				_, writeErr := file.Write(append(newEvent, '\n'))
				_ = file.Close()
				if writeErr != nil {
					return "", writeErr
				}
			}
			return "darwin:1:2", nil
		},
		Run: func(string, ...string) ([]byte, error) { return nil, fmt.Errorf("unexpected external command") },
	}
	if _, err := r.Locate("gjc", []int{100}); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("same-PID session switch was accepted: %v", err)
	}
}
func offlineGJCResolver(agentDir, identity string) Resolver {
	return Resolver{
		GJCDir:   agentDir,
		Identity: func(int) (string, error) { return identity, nil },
		Run: func(string, ...string) ([]byte, error) {
			return nil, fmt.Errorf("unexpected external command")
		},
	}
}

func writeGJCHeader(t *testing.T, agentDir, sessionID string) string {
	t.Helper()
	path := filepath.Join(agentDir, "sessions", "project", "date_"+sessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	header := fmt.Sprintf(`{"type":"session","version":5,"id":%q}`+"\n", sessionID)
	if err := os.WriteFile(path, []byte(header), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestGJCOfflineHeartbeatRemainsIndependentOfLaterLifecycle(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UnixMilli()
	root := filepath.Join(dir, "state", "main")
	events := [][]byte{
		gjcTestEvent(t, 4, 1, "main", "host_registered", 100, 1, root, "darwin:1:2", now-200000, false, false),
		gjcTestEvent(t, 4, 2, "main", "host_heartbeat", 100, 1, root, "darwin:1:2", now, false, false),
		gjcTestEvent(t, 4, 3, "main", "lifecycle_started", 100, 1, root, "darwin:1:2", now, false, false),
	}
	writeGJCIndexFixture(t, dir, false, 0, nil, events)
	want := writeGJCHeader(t, dir, "main")
	got, err := offlineGJCResolver(dir, "darwin:1:2").Locate("gjc", []int{100})
	if err != nil || got.Path != want {
		t.Fatalf("later lifecycle discarded fresh heartbeat: %+v %v", got, err)
	}
}

func TestGJCOfflineUncertainOrDeadCompetingRootFences(t *testing.T) {
	for _, kind := range []string{"host_registered", "lifecycle_terminal"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			now := time.Now().UnixMilli()
			events := [][]byte{
				gjcTestEvent(t, 4, 1, "main", "host_registered", 100, 1, filepath.Join(dir, "live"), "darwin:1:2", now, false, false),
				gjcTestEvent(t, 4, 2, "main", kind, 999, 1, filepath.Join(dir, "other"), "darwin:old", now-200000, false, false),
			}
			writeGJCIndexFixture(t, dir, false, 0, nil, events)
			writeGJCHeader(t, dir, "main")
			if _, err := offlineGJCResolver(dir, "darwin:1:2").Locate("gjc", []int{100}); err == nil {
				t.Fatal("unresolved competing root was ignored")
			}
		})
	}
}

func TestGJCOfflineFreshnessBoundary(t *testing.T) {
	now := time.UnixMilli(1800000000000)
	for _, age := range []int64{119999, 120000, 120001} {
		dir := t.TempDir()
		event := gjcTestEvent(t, 4, 1, "main", "host_registered", 100, 1, filepath.Join(dir, "state"), "darwin:1:2", now.UnixMilli()-age, false, false)
		writeGJCIndexFixture(t, dir, false, 0, nil, [][]byte{event})
		events, err := gjcReadIndex(dir)
		if err != nil {
			t.Fatal(err)
		}
		projection := gjcReduceIndex(events, dir, now)
		if len(projection.sessions) != 1 || projection.sessions[0].live != (age < 120000) {
			t.Fatalf("age=%d incorrect liveness: %+v", age, projection.sessions)
		}
	}
}

func TestGJCOfflineAdmissionRejectsOldIncarnationAndTombstoneRevival(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UnixMilli()
	root := filepath.Join(dir, "state")
	events := [][]byte{
		gjcTestEvent(t, 4, 1, "main", "host_registered", 100, 1, root, "darwin:old", now, false, false),
		gjcTestEvent(t, 4, 2, "main", "host_registered", 100, 1, root, "darwin:1:2", now, false, false),
		gjcTestEvent(t, 4, 3, "main", "host_unregistered", 100, 1, root, "darwin:old", now, false, false),
	}
	writeGJCIndexFixture(t, dir, false, 0, nil, events)
	want := writeGJCHeader(t, dir, "main")
	got, err := offlineGJCResolver(dir, "darwin:1:2").Locate("gjc", []int{100})
	if err != nil || got.Path != want {
		t.Fatalf("old incarnation retired current host: %+v %v", got, err)
	}
	events = append(events,
		gjcTestEvent(t, 4, 4, "main", "session_deleted", 100, 1, root, "darwin:1:2", now, false, false),
		gjcTestEvent(t, 4, 5, "main", "record_reconciled", 100, 1, root, "darwin:1:2", now, false, false),
		gjcTestEvent(t, 4, 6, "main", "host_heartbeat", 100, 1, root, "darwin:1:2", now, false, false),
	)
	writeGJCIndexFixture(t, dir, false, 0, nil, events)
	if _, err := offlineGJCResolver(dir, "darwin:1:2").Locate("gjc", []int{100}); err == nil {
		t.Fatal("late events resurrected deleted session")
	}
}
