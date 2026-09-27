package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	agents "agent-transcript/internal/harness"
	"golang.org/x/sys/unix"
)

var testBinary string

func TestMain(m *testing.M) {
	if os.Getenv("AGENT_TRANSCRIPT_SOURCE_HELPER") == "1" {
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "agent-transcript-test-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	testBinary = filepath.Join(dir, "agent-transcript")
	cmd := exec.Command("go", "build", "-o", testBinary, ".")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err = cmd.Run(); err != nil {
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
func TestParse(t *testing.T) {
	for _, args := range [][]string{{"--harness", "auto"}, {"--transcript", "session.jsonl"}, {"--gjc-dir", "x"}, {"--claude-dir", "x"}, {"--leaf-id", "a"}, {"--codex"}, {"%1", "%2"}, {"--unknown"}} {
		if _, err := parse(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	got, err := parse([]string{"%3", "--reader=leaf"})
	if err != nil || got.target != "%3" || got.reader != "leaf" {
		t.Fatalf("%+v %v", got, err)
	}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"--help"}, &out); err != nil || strings.Contains(out.String(), "--harness") || strings.Contains(out.String(), "--transcript") {
		t.Fatal(out.String(), err)
	}
}
func TestSourceHelper(t *testing.T) {
	if os.Getenv("AGENT_TRANSCRIPT_SOURCE_HELPER") != "1" {
		return
	}
	for _, path := range filepath.SplitList(os.Getenv("AGENT_TRANSCRIPT_OPEN_FILES")) {
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
	}
	for i := 0; i < 150; i++ {
		fmt.Printf("row-%04d 한글 snapshot\n", i)
	}
	fmt.Println("READY")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		fmt.Println("ECHO:" + scanner.Text())
	}
}

type server struct {
	t                   *testing.T
	dir, socket, source string
	env                 []string
}

func setEnv(env []string, changes map[string]string) []string {
	values := map[string]string{}
	for _, entry := range env {
		k, v, ok := strings.Cut(entry, "=")
		if ok {
			values[k] = v
		}
	}
	for k, v := range changes {
		values[k] = v
	}
	delete(values, "TMUX_PANE")
	delete(values, "TMUX")
	result := []string{}
	for k, v := range values {
		result = append(result, k+"="+v)
	}
	sort.Strings(result)
	return result
}
func newServer(t *testing.T, agentName ...string) *server {
	t.Helper()
	for _, tool := range []string{"tmux", "leaf", "lsof"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("requires %s", tool)
		}
	}
	dir, err := os.MkdirTemp("", "at-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if err := os.Mkdir(filepath.Join(dir, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	s := &server{t: t, dir: dir, socket: filepath.Join(dir, "tmux.sock")}
	s.env = setEnv(os.Environ(), map[string]string{"HOME": dir, "TMPDIR": filepath.Join(dir, "tmp"), "XDG_CONFIG_HOME": filepath.Join(dir, "config"), "GJC_CODING_AGENT_DIR": filepath.Join(dir, "gjc"), "CLAUDE_CONFIG_DIR": filepath.Join(dir, "claude"), "TERM": "xterm-256color", "AGENT_TRANSCRIPT_SOURCE_HELPER": "1"})
	s.env = append(s.env, "XDG_CACHE_HOME="+filepath.Join(dir, "cache"))
	t.Cleanup(func() { s.rawTmux("kill-server") })
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if len(agentName) > 0 {
		data, err := os.ReadFile(exe)
		if err != nil {
			t.Fatal(err)
		}
		exe = filepath.Join(dir, "bin", agentName[0])
		if err := os.MkdirAll(filepath.Dir(exe), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(exe, data, 0700); err != nil {
			t.Fatal(err)
		}
	}
	s.source = s.tmux("-f", "/dev/null", "new-session", "-d", "-s", "test", "-x", "160", "-y", "30", "-P", "-F", "#{pane_id}", quote(exe)+" -test.run=^TestSourceHelper$")
	s.env = append(s.env, "TMUX="+s.socket+",0,0", "TMUX_PANE="+s.source)
	s.wait(func() bool { return strings.Contains(s.capture(s.source), "READY") })
	return s
}
func (s *server) rawTmux(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", append([]string{"-S", s.socket}, args...)...)
	cmd.Env = s.env
	output, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}
func (s *server) tmux(args ...string) string {
	s.t.Helper()
	out, err := s.rawTmux(args...)
	if err != nil {
		s.t.Fatalf("tmux %v: %s %v", args, out, err)
	}
	return out
}
func (s *server) capture(pane string) string { return s.tmux("capture-pane", "-p", "-t", pane) }
func (s *server) panes() []string {
	return strings.Fields(s.tmux("list-panes", "-t", "test", "-F", "#{pane_id}"))
}
func (s *server) wait(predicate func() bool) {
	s.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.t.Fatal("timed out waiting for tmux")
}
func (s *server) launch(args ...string) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, testBinary, args...)
	cmd.Env = s.env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}
func (s *server) open(args ...string) string {
	s.t.Helper()
	stdout, stderr, err := s.launch(args...)
	if err != nil || stdout != "" || stderr != "" {
		s.t.Fatalf("launch: stdout=%q stderr=%q err=%v", stdout, stderr, err)
	}
	panes := s.panes()
	if len(panes) != 2 {
		s.t.Fatalf("expected viewer, got %v", panes)
	}
	for _, pane := range panes {
		if pane != s.source {
			s.wait(func() bool { return strings.Contains(s.capture(pane), "leaf  stdin") })
			return pane
		}
	}
	s.t.Fatal("viewer missing")
	return ""
}
func (s *server) snapshotRoot() string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(s.dir, "Library", "Caches", "agent-transcript", "snapshots")
	}
	return filepath.Join(s.dir, "cache", "agent-transcript", "snapshots")
}
func (s *server) clean() {
	entries, err := os.ReadDir(s.snapshotRoot())
	if err != nil && !os.IsNotExist(err) {
		s.t.Fatal(err)
	}
	if len(entries) != 0 {
		s.t.Fatalf("snapshot cache not cleaned: %v", entries)
	}
	s.t.Helper()
	entries, err = os.ReadDir(filepath.Join(s.dir, "tmp"))
	if err != nil || len(entries) != 0 {
		s.t.Fatalf("temporary files remain: %v %v", entries, err)
	}
}
func (s *server) close(pane string) {
	s.tmux("send-keys", "-t", pane, "Escape", "q")
	s.wait(func() bool { return len(s.panes()) == 1 })
	s.clean()
}
func writeJSON(t *testing.T, path string, records ...map[string]any) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, record := range records {
		if err := json.NewEncoder(f).Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	return path
}
func TestScreenViewerNavigationFocusAndCleanup(t *testing.T) {
	s := newServer(t)
	pane := s.open()
	if s.tmux("display-message", "-p", "-t", "test", "#{pane_id}") != pane {
		t.Fatal("viewer not focused")
	}
	s.wait(func() bool { return strings.Contains(s.capture(pane), "READY") })
	if !strings.Contains(s.capture(pane), "한글 snapshot") {
		t.Fatal("Unicode text lost at bottom")
	}
	s.clean()
	s.tmux("send-keys", "-t", pane, "G")
	s.wait(func() bool { return strings.Contains(s.capture(pane), "READY") })
	s.tmux("send-keys", "-t", s.source, "NEW-QUESTION", "Enter")
	s.wait(func() bool { return strings.Contains(s.capture(s.source), "ECHO:NEW-QUESTION") })
	if strings.Contains(s.capture(pane), "NEW-QUESTION") {
		t.Fatal("snapshot unexpectedly updated")
	}
	s.tmux("send-keys", "-t", pane, "g", "/row-0075", "Enter")
	s.wait(func() bool { return strings.Contains(s.capture(pane), "row-0075") })
	s.close(pane)
	if s.tmux("display-message", "-p", "-t", "test", "#{pane_id}") != s.source {
		t.Fatal("source focus not restored")
	}
}
func TestCopyModeAndForcedClose(t *testing.T) {
	s := newServer(t)
	s.tmux("copy-mode", "-t", s.source)
	s.tmux("send-keys", "-X", "-t", s.source, "history-top")
	pane := s.open()
	if s.tmux("display-message", "-p", "-t", s.source, "#{pane_in_mode}") != "1" {
		t.Fatal("copy mode lost")
	}
	s.tmux("kill-pane", "-t", pane)
	s.clean()
}

func TestUnknownForegroundFallsBackToScreenWithoutAdoptingAgentTranscript(t *testing.T) {
	path := writeJSON(t, filepath.Join(t.TempDir(), "rollout-main.jsonl"),
		map[string]any{"type": "session_meta", "payload": map[string]any{"id": "main", "source": "cli"}},
		map[string]any{"type": "event_msg", "payload": map[string]any{"type": "item_completed", "thread_id": "main", "item": map[string]any{"type": "AgentMessage", "id": "a", "content": []any{map[string]any{"type": "Text", "text": "Explicitly chosen answer"}}}}})
	t.Setenv("AGENT_TRANSCRIPT_OPEN_FILES", path)
	s := newServer(t, "yazi")
	pane := s.open()
	s.wait(func() bool { return strings.Contains(s.capture(pane), "READY") })
	if title := s.tmux("display-message", "-p", "-t", pane, "#{pane_title}"); title != "Screen capture of "+s.source {
		t.Fatalf("screen fallback title = %q", title)
	}
	if strings.Contains(s.capture(pane), "Explicitly chosen answer") {
		t.Fatal("unsupported TUI adopted an owned agent transcript")
	}
	s.close(pane)
}
func TestRecognizedForegroundWithoutSessionHasDistinctError(t *testing.T) {
	s := newServer(t, "codex")
	_, stderr, err := s.launch()
	if err == nil || !strings.Contains(stderr, "identified codex") || !strings.Contains(stderr, "found 0 sessions") {
		t.Fatalf("wrong discovery-stage error: %s %v", stderr, err)
	}
	s.clean()
}
func TestLaunchFailuresCleanup(t *testing.T) {
	s := newServer(t)
	for _, args := range [][]string{{"%999999"}, {"--bad"}} {
		_, stderr, err := s.launch(args...)
		if err == nil || stderr == "" {
			t.Fatalf("accepted %v", args)
		}
		if len(s.panes()) != 1 {
			t.Fatal("failure left pane")
		}
		s.clean()
	}
	s.tmux("resize-window", "-t", "test", "-x", "2", "-y", "30")
	if _, _, err := s.launch(); err == nil {
		t.Fatal("split should fail")
	}
	s.clean()

	t.Run("malformed identified transcript", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "rollout-main.jsonl")
		if err := os.WriteFile(path, []byte("not json\n"), 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("AGENT_TRANSCRIPT_OPEN_FILES", path)
		s := newServer(t, "codex")
		_, stderr, err := s.launch()
		if err == nil || !strings.Contains(strings.ToLower(stderr), "json") {
			t.Fatalf("malformed identified transcript accepted: %s %v", stderr, err)
		}
		if len(s.panes()) != 1 {
			t.Fatal("failure left pane")
		}
		s.clean()
	})
}
func TestFailedViewerAndRemainOnExit(t *testing.T) {
	t.Run("failed shell", func(t *testing.T) {
		s := newServer(t)
		falsePath, err := exec.LookPath("false")
		if err != nil {
			t.Fatal(err)
		}
		s.tmux("set-option", "-g", "default-shell", falsePath)
		if _, _, err := s.launch(); err == nil {
			t.Fatal("failed viewer accepted")
		}
		s.wait(func() bool { return len(s.panes()) == 1 })
		s.clean()
	})
	t.Run("remain on exit", func(t *testing.T) {
		s := newServer(t)
		s.tmux("set-option", "-w", "-t", "test", "remain-on-exit", "on")
		s.close(s.open())
	})
}
func writeGJCRegistration(t *testing.T, s *server, pid int, identity string) {
	t.Helper()
	record := map[string]any{
		"version": 4, "type": "host_registered", "indexSeq": 1, "sessionId": "main", "pid": pid,
		"processIncarnation": identity, "hostIncarnation": identity, "endpointGeneration": 1,
		"ts":      time.Now().UnixMilli(),
		"locator": map[string]any{"cwd": s.dir, "worktreeRoot": nil, "stateRoot": filepath.Join(s.dir, ".gjc", "state")},
	}
	unsigned, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	record["checksum"] = fmt.Sprintf("%x", sha256.Sum256(unsigned))
	writeJSON(t, filepath.Join(s.dir, "gjc", "sdk", "sessions", "index.jsonl"), record)
}
func autoFixtureServer(t *testing.T, harness string, records ...map[string]any) (*server, string) {
	t.Helper()
	if harness == "codex" {
		path := writeJSON(t, filepath.Join(t.TempDir(), "rollout-main.jsonl"), records...)
		t.Setenv("AGENT_TRANSCRIPT_OPEN_FILES", path)
		return newServer(t, "codex"), path
	}
	s := newServer(t, harness)
	var path string
	switch harness {
	case "claude":
		path = writeJSON(t, filepath.Join(s.dir, "claude", "projects", "project", "main.jsonl"), records...)
		pid, err := strconv.Atoi(s.tmux("display-message", "-p", "-t", s.source, "#{pane_pid}"))
		if err != nil {
			t.Fatal(err)
		}
		start, err := agents.Run("ps", "-p", strconv.Itoa(pid), "-o", "lstart=")
		if err != nil {
			t.Fatal(err)
		}
		writeJSON(t, filepath.Join(s.dir, "claude", "sessions", strconv.Itoa(pid)+".json"), map[string]any{
			"pid": pid, "sessionId": "main", "procStart": strings.TrimSpace(string(start)),
			"pidDomain": runtime.GOOS, "kind": "interactive", "entrypoint": "cli",
		})
	case "gjc":
		pid, err := strconv.Atoi(s.tmux("display-message", "-p", "-t", s.source, "#{pane_pid}"))
		if err != nil {
			t.Fatal(err)
		}
		identity, err := agents.ProcessIdentity(pid)
		if err != nil {
			t.Fatal(err)
		}
		path = writeJSON(t, filepath.Join(s.dir, "gjc", "sessions", "project", "date_main.jsonl"), records...)
		writeGJCRegistration(t, s, pid, identity)
	default:
		t.Fatalf("unsupported fixture harness %q", harness)
	}
	return s, path
}

func TestSupportedHarnessesExplicitRendering(t *testing.T) {
	for _, harness := range []string{"codex", "gjc"} {
		t.Run(harness, func(t *testing.T) {
			var records []map[string]any
			switch harness {
			case "codex":
				records = []map[string]any{{"type": "session_meta", "payload": map[string]any{"id": "main", "source": "cli"}}, {"type": "event_msg", "payload": map[string]any{"type": "item_completed", "thread_id": "main", "turn_id": "t", "item": map[string]any{"type": "AgentMessage", "id": "a", "content": []any{map[string]any{"type": "Text", "text": "**Clean answer**"}}}}}}
			case "gjc":
				records = []map[string]any{{"type": "session", "version": 5, "id": "main"}, {"type": "message", "id": "a", "parentId": nil, "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "**Clean answer**"}}, "stopReason": "stop"}}}
			}
			s, path := autoFixtureServer(t, harness, records...)
			pane := s.open()
			screen := s.capture(pane)
			if !strings.Contains(screen, "Clean answer") || strings.Contains(screen, "**Clean answer**") || strings.Contains(screen, "row-0000") {
				t.Fatal("transcript not rendered correctly:", screen)
			}
			s.close(pane)
			if _, err := os.Stat(path); err != nil {
				t.Fatal("original transcript removed")
			}
		})
	}
}

func TestCodexOpenFileDiscovery(t *testing.T) {
	records := []map[string]any{{"type": "session_meta", "payload": map[string]any{"id": "main", "source": "cli"}}, {"type": "event_msg", "payload": map[string]any{"type": "item_completed", "thread_id": "main", "item": map[string]any{"type": "AgentMessage", "id": "a", "content": []any{map[string]any{"type": "Text", "text": "Owned Codex answer"}}}}}}
	s, _ := autoFixtureServer(t, "codex", records...)
	pane := s.open()
	if !strings.Contains(s.capture(pane), "Owned Codex answer") {
		t.Fatal("owned transcript not displayed")
	}
	s.close(pane)
}
func TestBinaryAndBindingPathsWithShellCharacters(t *testing.T) {
	s := newServer(t)
	root := filepath.Join(s.dir, "plugin ' space $d")
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "tmux"), 0700); err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(testBinary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin/agent-transcript"), binary, 0700); err != nil {
		t.Fatal(err)
	}
	config, err := os.ReadFile("../../tmux/agent-transcript.tmux")
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "tmux/agent-transcript.tmux")
	if err := os.WriteFile(configPath, config, 0600); err != nil {
		t.Fatal(err)
	}
	s.tmux("source-file", configPath)
	for _, table := range []string{"prefix", "copy-mode", "copy-mode-vi"} {
		if !strings.Contains(s.tmux("list-keys", "-T", table), "agent_transcript_command") {
			t.Fatal("missing binding", table)
		}
	}
	s.tmux("run-shell", "-t", s.source, "#{q:@agent_transcript_command} '#{pane_id}'")
	panes := s.panes()
	if len(panes) != 2 {
		t.Fatal("viewer launch failed", panes)
	}
	for _, pane := range panes {
		if pane != s.source {
			s.wait(func() bool { return strings.Contains(s.capture(pane), "leaf  stdin") })
			s.close(pane)
		}
	}
}
func TestClaudeNativeFallsBackToScreenWithDiscoverableRegistry(t *testing.T) {
	s, path := autoFixtureServer(t, "claude", map[string]any{"type": "assistant", "sessionId": "main", "uuid": "a", "parentUuid": nil,
		"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "Claude registry answer"}}}})
	pane := s.open()
	s.wait(func() bool { return strings.Contains(s.capture(pane), "READY") })
	if title := s.tmux("display-message", "-p", "-t", pane, "#{pane_title}"); title != "Screen capture of "+s.source {
		t.Fatalf("Claude fallback title = %q", title)
	}
	if strings.Contains(s.capture(pane), "Claude registry answer") {
		t.Fatal("discoverable Claude registry transcript was rendered")
	}
	s.close(pane)
	if _, err := os.Stat(path); err != nil {
		t.Fatal("original Claude file removed")
	}
}
func TestGJCAutomaticMapping(t *testing.T) {
	s := newServer(t, "gjc")
	pid, _ := strconv.Atoi(s.tmux("display-message", "-p", "-t", s.source, "#{pane_pid}"))
	identity, err := agents.ProcessIdentity(pid)
	if err != nil {
		t.Skip(err)
	}
	writeJSON(t, filepath.Join(s.dir, "gjc/sessions/project/date_main.jsonl"), map[string]any{"type": "session", "version": 5, "id": "main"}, map[string]any{"type": "message", "id": "a", "parentId": nil, "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "GJC automatic answer"}}}})
	writeGJCRegistration(t, s, pid, identity)
	pane := s.open()
	if !strings.Contains(s.capture(pane), "GJC automatic answer") {
		t.Fatal("wrong GJC session")
	}
	s.close(pane)
}

func terminalOutput(t *testing.T, file *os.File, duration time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(duration)
	var output []byte
	for time.Now().Before(deadline) {
		poll := []unix.PollFd{{Fd: int32(file.Fd()), Events: unix.POLLIN}}
		n, err := unix.Poll(poll, int(time.Until(deadline).Milliseconds()))
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
		buffer := make([]byte, 65536)
		count, err := unix.Read(int(file.Fd()), buffer)
		if err == unix.EIO || count == 0 {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		output = append(output, buffer[:count]...)
	}
	return output
}
func TestAttachedClientFocusAndQuietBoundaries(t *testing.T) {
	s := newServer(t)
	tty, _ := attachTestClient(t, s)
	pane := s.open()
	terminalOutput(t, tty, 200*time.Millisecond)
	if s.tmux("display-message", "-p", "-t", "test", "#{pane_id}") != pane {
		t.Fatal("viewer not focused")
	}
	for _, keys := range []string{"gkkk", "Gjjj"} {
		if _, err := tty.Write([]byte(keys)); err != nil {
			t.Fatal(err)
		}
		output := terminalOutput(t, tty, 300*time.Millisecond)
		if bytes.Contains(output, []byte{7}) || bytes.Contains(output, []byte("\x1b[?5h")) {
			t.Fatalf("pager rang or flashed: %q", output)
		}
	}
	s.wait(func() bool { return strings.Contains(s.capture(pane), "READY") })
	tty.Write([]byte("q"))
	s.wait(func() bool { return len(s.panes()) == 1 })
	s.clean()
}
