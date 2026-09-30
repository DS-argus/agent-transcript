package harness

import (
	"agent-transcript/internal/agents"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"agent-transcript/internal/transcript"
)

func TestRegisteredAdapterProvidesAllFourStages(t *testing.T) {
	old := adapters
	t.Cleanup(func() { adapters = old })
	path := writeRecords(t, filepath.Join(t.TempDir(), "session.jsonl"), map[string]any{"type": "example-session", "id": "example-id"})
	adapters = append(append([]Adapter{}, adapters...), Adapter{
		Name: "example", Executables: []string{"example-cli"},
		Detect: func(records []map[string]any) (string, bool) {
			return "example-id", len(records) > 0 && transcript.String(records[0], "type") == "example-session"
		},
		Locate: func(_ Resolver, pids []int) (agents.Source, error) {
			if !reflect.DeepEqual(pids, []int{123}) {
				t.Fatalf("wrong owners: %v", pids)
			}
			return agents.Source{Harness: "example", Path: path}, nil
		},
		Render: func(renderPath string) (transcript.Document, error) {
			if renderPath != path {
				t.Fatal("session path not forwarded")
			}
			return transcript.NewDocument("example", "example-id", []string{"## User\n\nexample text"}, false)
		},
	})
	if name, ok := HarnessForExecutable("/opt/bin/example-cli"); !ok || name != "example" {
		t.Fatal("registered executable not identified")
	}
	if !strings.Contains(strings.Join(Names(), ","), "example") {
		t.Fatal("registered harness missing from help names")
	}
	source, err := (Resolver{}).Locate("example", []int{123})
	if err != nil || source.Path != path {
		t.Fatalf("%+v %v", source, err)
	}
	doc, err := RenderFile("example", path)
	if err != nil || doc.Harness != "example" {
		t.Fatalf("%+v %v", doc, err)
	}
}

func TestNodeScriptMatchingResolvesInstalledSymlink(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "node_modules/@openai/codex/bin/codex.js")
	if err := os.MkdirAll(filepath.Dir(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("// test script"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "codex")
	if err := os.Symlink(script, link); err != nil {
		t.Fatal(err)
	}
	if name, ok := HarnessForNodeScript(link); !ok || name != "codex" {
		t.Fatalf("%q %v", name, ok)
	}
	for _, path := range []string{"/tmp/codex.js", "/tmp/tui.js", "/@openai/codex/bin/codex.js.other"} {
		if _, ok := HarnessForNodeScript(path); ok {
			t.Fatalf("unregistered script matched %s", path)
		}
	}
}

func TestResolveOnlyReadsIdentifiedHarnessAndOwner(t *testing.T) {
	root := t.TempDir()
	file := writeRecords(t, filepath.Join(root, "gjc/sessions/project/date_main.jsonl"), map[string]any{"type": "session", "version": 5, "id": "main"})
	rows := make([]map[string]any, 0, 2)
	for i, sessionID := range []string{"main", "worker"} {
		row := map[string]any{
			"version": 4, "type": "host_registered", "indexSeq": i + 1, "sessionId": sessionID, "pid": 101 + i,
			"hostIncarnation": "darwin:10:20", "processIncarnation": "darwin:10:20", "endpointGeneration": 1,
			"ts":      time.Now().UnixMilli(),
			"locator": map[string]any{"cwd": root, "worktreeRoot": nil, "stateRoot": filepath.Join(root, ".gjc", "state")},
		}
		data, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		row["checksum"] = fmt.Sprintf("%x", sha256.Sum256(data))
		rows = append(rows, row)
	}
	writeRecords(t, filepath.Join(root, "gjc/sdk/sessions/index.jsonl"), rows...)
	if err := os.MkdirAll(filepath.Join(root, "claude/sessions"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "claude/sessions/101.json"), []byte("corrupt unrelated registry"), 0600); err != nil {
		t.Fatal(err)
	}
	r := Resolver{GJCDir: filepath.Join(root, "gjc"), ClaudeDir: filepath.Join(root, "claude"), Identity: func(pid int) (string, error) {
		if pid != 101 {
			t.Fatalf("inspected nested worker %d", pid)
		}
		return "darwin:10:20", nil
	}, Run: func(name string, args ...string) ([]byte, error) {
		if name == "ps" && reflect.DeepEqual(args, []string{"-axo", "pid=,ppid=,pgid=,tpgid=,comm="}) {
			return []byte("100 1 100 101 -zsh\n101 100 101 101 gjc\n102 101 101 101 codex\n"), nil
		}
		return nil, fmt.Errorf("unexpected inspection: %s %v", name, args)
	}}
	got, err := r.Resolve(100)
	if err != nil || got != (agents.Source{Harness: "gjc", Path: file}) {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestUnsupportedForegroundStopsBeforeAnySessionInspection(t *testing.T) {
	for _, name := range []string{"yazi", "nvim", "unknown-tui"} {
		t.Run(name, func(t *testing.T) {
			r := Resolver{Run: func(command string, args ...string) ([]byte, error) {
				if command == "ps" && reflect.DeepEqual(args, []string{"-axo", "pid=,ppid=,pgid=,tpgid=,comm="}) {
					return []byte("100 1 100 101 -zsh\n101 100 101 101 " + name + "\n102 101 101 101 codex\n"), nil
				}
				t.Fatalf("session inspection for unsupported TUI: %s %v", command, args)
				return nil, nil
			}}
			if _, err := r.Resolve(100); err == nil {
				t.Fatal("adopted nested agent")
			}
		})
	}
}

func TestAllSupportedAgentsHaveRegisteredContracts(t *testing.T) {
	if got := Names(); !reflect.DeepEqual(got, []string{"codex", "gjc", "claude"}) {
		t.Fatalf("unexpected agents: %v", got)
	}
	if _, ok := Lookup("claude"); !ok {
		t.Fatal("Claude adapter missing")
	}
	if name, ok := HarnessForExecutable("/usr/local/bin/claude"); !ok || name != "claude" {
		t.Fatal("Claude executable missing")
	}
	if name, ok := HarnessForNodeScript("/opt/node_modules/@anthropic-ai/claude-code/cli.js"); !ok || name != "claude" {
		t.Fatal("Claude launcher missing")
	}
	path := writeRecords(t, filepath.Join(t.TempDir(), "claude.jsonl"), map[string]any{
		"type": "user", "sessionId": "main", "uuid": "u", "parentUuid": nil,
		"message": map[string]any{"role": "user", "content": "Registered Claude parser"},
	})
	kind, id, err := DetectFile(path)
	if err != nil || kind != "claude" || id != "main" {
		t.Fatalf("Claude detection: %s %s %v", kind, id, err)
	}
	if doc, err := RenderFile("claude", path); err != nil || !strings.Contains(doc.Markdown, "Registered Claude parser") {
		t.Fatalf("Claude render: %+v %v", doc, err)
	}
}
