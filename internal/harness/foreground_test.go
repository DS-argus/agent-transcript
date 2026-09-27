package harness

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestIdentifyForegroundDirectHarnesses(t *testing.T) {
	for _, test := range []struct {
		name    string
		command string
		harness string
	}{
		{name: "codex", command: "codex", harness: "codex"},
		{name: "gjc", command: "gjc", harness: "gjc"},
	} {
		t.Run(test.name, func(t *testing.T) {
			table := fmt.Sprintf("100 1 100 200 zsh\n200 100 200 200 %s\n", test.command)
			runner := foregroundFakeRun(table, nil)
			got, err := IdentifyForeground(100, runner)
			if err != nil {
				t.Fatalf("IdentifyForeground: %v", err)
			}
			want := Foreground{Harness: test.harness, PIDs: []int{200}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("foreground = %#v, want %#v", got, want)
			}
		})
	}
}

func TestIdentifyForegroundRejectsClaudeSentinels(t *testing.T) {
	for _, test := range []struct {
		name   string
		table  string
		output map[string]string
	}{
		{name: "native", table: "100 1 100 200 zsh\n200 100 200 200 claude\n"},
		{name: "node", table: "100 1 100 200 zsh\n200 100 200 200 node\n", output: map[string]string{
			"ps -p 200 -o args=": "node /opt/node_modules/@anthropic-ai/claude-code/cli.js\n",
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := IdentifyForeground(100, foregroundFakeRun(test.table, test.output))
			if err == nil || !errors.Is(err, ErrUnsupportedForeground) {
				t.Fatalf("error = %v, want unsupported Claude foreground", err)
			}
		})
	}
}

func TestIdentifyForegroundCodexNodeNativePair(t *testing.T) {
	table := strings.Join([]string{
		"100 1 100 200 zsh",
		"200 100 200 200 node",
		"201 200 200 200 codex",
		"202 200 200 200 mcp-server",
		"300 100 300 300 codex",
		"400 999 400 400 codex",
	}, "\n") + "\n"
	runner := foregroundFakeRun(table, map[string]string{
		"ps -p 200 -o args=": "node /opt/homebrew/lib/node_modules/@openai/codex/bin/codex.js --yolo\n",
	})
	got, err := IdentifyForeground(100, runner)
	if err != nil {
		t.Fatalf("IdentifyForeground: %v", err)
	}
	want := Foreground{Harness: "codex", PIDs: []int{200, 201}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("foreground = %#v, want %#v", got, want)
	}
}

func TestIdentifyForegroundShellIdleIgnoresBackgroundAgent(t *testing.T) {
	table := "100 1 100 100 zsh\n200 100 200 200 codex\n"
	_, err := IdentifyForeground(100, foregroundFakeRun(table, nil))
	if err == nil || !errors.Is(err, ErrUnsupportedForeground) {
		t.Fatalf("error = %v, want unsupported foreground TUI", err)
	}
}

func TestIdentifyForegroundNativeBoundaryIgnoresNestedAgent(t *testing.T) {
	table := "100 1 100 200 zsh\n200 100 200 200 gjc\n201 200 200 200 codex\n"
	got, err := IdentifyForeground(100, foregroundFakeRun(table, nil))
	if err != nil {
		t.Fatalf("IdentifyForeground: %v", err)
	}
	want := Foreground{Harness: "gjc", PIDs: []int{200}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("foreground = %#v, want %#v", got, want)
	}
}

func TestIdentifyForegroundNvimForegroundRejectsBackgroundCodex(t *testing.T) {
	table := "100 1 100 200 zsh\n200 100 200 200 nvim\n201 200 201 201 codex\n"
	_, err := IdentifyForeground(100, foregroundFakeRun(table, nil))
	if err == nil || !errors.Is(err, ErrUnsupportedForeground) {
		t.Fatalf("error = %v, want unsupported foreground TUI", err)
	}
}

func TestIdentifyForegroundRejectsUnrelatedForegroundOwner(t *testing.T) {
	table := "100 1 100 200 zsh\n200 999 200 200 codex\n"
	_, err := IdentifyForeground(100, foregroundFakeRun(table, nil))
	if err == nil || errors.Is(err, ErrUnsupportedForeground) {
		t.Fatalf("error = %v, want foreground ownership error", err)
	}
}

func TestIdentifyForegroundRejectsMissingForegroundOwner(t *testing.T) {
	table := "100 1 100 200 zsh\n201 100 201 201 nvim\n"
	_, err := IdentifyForeground(100, foregroundFakeRun(table, nil))
	if err == nil || errors.Is(err, ErrUnsupportedForeground) {
		t.Fatalf("error = %v, want foreground ownership error", err)
	}
}

func TestIdentifyForegroundRejectsInvalidPSRows(t *testing.T) {
	for _, table := range []string{
		"100 1 100 100\n",
		"not-a-pid 1 100 100 zsh\n",
		"100 1 100 100 zsh\n100 1 100 100 codex\n",
	} {
		t.Run(strings.TrimSpace(table), func(t *testing.T) {
			_, err := IdentifyForeground(100, foregroundFakeRun(table, nil))
			if err == nil || !strings.Contains(err.Error(), "invalid ps") {
				t.Fatalf("error = %v, want invalid ps row", err)
			}
		})
	}
}

func TestIdentifyForegroundRejectsUnsupportedNodeWrappers(t *testing.T) {
	tests := []struct {
		name string
		args string
	}{
		{name: "unknown script", args: "node /tmp/other.js\n"},
		{name: "eval is not script", args: "node --eval 'require(\"/@openai/codex/bin/codex.js\")'\n"},
		{name: "eval equals followed by fake script", args: "node --eval=0 /opt/@openai/codex/bin/codex.js"},
		{name: "short eval followed by fake script", args: "node -e0 /opt/@openai/codex/bin/codex.js"},
		{name: "unknown mode is not script", args: "node --unknown-mode /opt/@openai/codex/bin/codex.js"},
		{name: "test runner is not CLI", args: "node --test /opt/@openai/codex/bin/codex.js"},
		{name: "unterminated command", args: "node '/@openai/codex/bin/codex.js\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			table := "100 1 100 200 zsh\n200 100 200 200 node\n"
			runner := foregroundFakeRun(table, map[string]string{"ps -p 200 -o args=": test.args})
			_, err := IdentifyForeground(100, runner)
			if err == nil || !errors.Is(err, ErrUnsupportedForeground) {
				t.Fatalf("error = %v, want unsupported foreground TUI", err)
			}
		})
	}
}

func TestIdentifyForegroundRejectsMultipleMatchingNativeChildren(t *testing.T) {
	table := "100 1 100 200 zsh\n200 100 200 200 node\n201 200 200 200 codex\n202 200 200 200 codex\n"
	runner := foregroundFakeRun(table, map[string]string{
		"ps -p 200 -o args=": "node /opt/@openai/codex/bin/codex.js\n",
	})
	_, err := IdentifyForeground(100, runner)
	if err == nil || !strings.Contains(err.Error(), "multiple same-harness native children") {
		t.Fatalf("error = %v, want ambiguous child error", err)
	}
}

func TestIdentifyForegroundAllowsSpacesInOtherProcessCommands(t *testing.T) {
	table := "100 1 100 200 -zsh\n200 100 200 200 /opt/bin/gjc\n300 1 300 0 /Applications/Google Chrome.app/Contents/Google Chrome Helper\n"
	got, err := IdentifyForeground(100, foregroundFakeRun(table, nil))
	if err != nil || got.Harness != "gjc" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestIdentifyForegroundFollowsActualForegroundJobChange(t *testing.T) {
	table := "100 1 100 201 zsh\n200 100 200 201 gjc\n201 200 201 201 codex\n"
	got, err := IdentifyForeground(100, foregroundFakeRun(table, nil))
	if err != nil || got.Harness != "codex" || !reflect.DeepEqual(got.PIDs, []int{201}) {
		t.Fatalf("%+v %v", got, err)
	}
}

func foregroundFakeRun(table string, commandOutput map[string]string) func(string, ...string) ([]byte, error) {
	return func(name string, args ...string) ([]byte, error) {
		key := name + " " + strings.Join(args, " ")
		if name == "ps" && len(args) == 2 && args[0] == "-axo" && args[1] == "pid=,ppid=,pgid=,tpgid=,comm=" {
			return []byte(table), nil
		}
		if output, ok := commandOutput[key]; ok {
			return []byte(output), nil
		}
		return nil, fmt.Errorf("unexpected process command %q", key)
	}
}

func TestForegroundInspectionErrorsDoNotAllowScreenFallback(t *testing.T) {
	for _, test := range []struct {
		name, table string
		output      map[string]string
	}{
		{name: "missing pane", table: "200 1 200 200 zsh\n"},
		{name: "missing group", table: "100 1 100 -1 zsh\n"},
		{name: "unrelated group", table: "100 1 100 200 zsh\n200 999 200 200 codex\n"},
		{name: "node inspection fails", table: "100 1 100 200 zsh\n200 100 200 200 node\n"},
		{name: "ambiguous recognized wrapper", table: "100 1 100 200 zsh\n200 100 200 200 node\n201 200 200 200 codex\n202 200 200 200 codex\n", output: map[string]string{"ps -p 200 -o args=": "node /opt/@openai/codex/bin/codex.js\n"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := IdentifyForeground(100, foregroundFakeRun(test.table, test.output))
			if err == nil || errors.Is(err, ErrUnsupportedForeground) {
				t.Fatalf("inspection error must not permit screen fallback: %v", err)
			}
		})
	}
	failure := errors.New("process inspection failed")
	_, err := IdentifyForeground(100, func(string, ...string) ([]byte, error) { return nil, failure })
	if !errors.Is(err, failure) || errors.Is(err, ErrUnsupportedForeground) {
		t.Fatalf("process error not preserved: %v", err)
	}
}
