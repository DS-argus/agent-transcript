package harness

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// ErrUnsupportedForeground means no supported agent owns the current TUI.
var ErrUnsupportedForeground = errors.New("unsupported foreground TUI")

// Foreground identifies the harness that owns a pane's current foreground
// process group. PIDs contains only the process (or the small wrapper chain)
// that can own that harness; background descendants are deliberately omitted.
type Foreground struct {
	Harness string
	PIDs    []int
}

type foregroundProcess struct {
	pid   int
	ppid  int
	pgid  int
	tpgid int
	comm  string
}

// IdentifyForeground finds the current foreground process group for panePID.
// The process table is intentionally the only broad process inspection. Once a
// node wrapper is found, only that wrapper's command line is inspected.
func IdentifyForeground(panePID int, run func(string, ...string) ([]byte, error)) (Foreground, error) {
	if panePID <= 0 {
		return Foreground{}, fmt.Errorf("invalid pane PID: %d", panePID)
	}
	if run == nil {
		return Foreground{}, fmt.Errorf("cannot identify foreground TUI: process runner is nil")
	}

	data, err := run("ps", "-axo", "pid=,ppid=,pgid=,tpgid=,comm=")
	if err != nil {
		return Foreground{}, err
	}
	processes, err := parseForegroundPS(string(data))
	if err != nil {
		return Foreground{}, err
	}
	root, ok := processes[panePID]
	if !ok {
		return Foreground{}, fmt.Errorf("pane process %d is not present in the process table", panePID)
	}

	members := paneProcessTree(panePID, processes)
	if _, ok := members[panePID]; !ok {
		return Foreground{}, fmt.Errorf("pane process %d has no process-tree owner", panePID)
	}
	group := root.tpgid
	if group <= 0 {
		return Foreground{}, fmt.Errorf("pane process %d has no foreground process group", panePID)
	}

	// A process-group leader is the process whose PID is the PGID. Requiring
	// that exact process to be in this pane's ancestry prevents a foreground
	// group from another pane from being selected by a coincidental PGID.
	leader, ok := members[group]
	if !ok || leader.pid != group || leader.pgid != group {
		return Foreground{}, fmt.Errorf("foreground process group %d has no owner in pane %d", group, panePID)
	}

	if harness, ok := HarnessForExecutable(leader.comm); ok {
		// A native TUI is the boundary. Its children may be tools, MCP
		// servers, or nested agents and must not affect auto detection.
		return Foreground{Harness: harness, PIDs: []int{leader.pid}}, nil
	}

	if !isNodeExecutable(leader.comm) {
		return Foreground{}, unsupportedForeground("foreground process %d (%s) is not a supported TUI", leader.pid, leader.comm)
	}

	args, err := run("ps", "-p", strconv.Itoa(leader.pid), "-o", "args=")
	if err != nil {
		return Foreground{}, fmt.Errorf("cannot inspect node wrapper process %d: %w", leader.pid, err)
	}
	script, ok := nodeScriptArgument(string(args))
	if !ok {
		return Foreground{}, unsupportedForeground("node process %d does not have a supported script entrypoint", leader.pid)
	}
	harness, ok := HarnessForNodeScript(script)
	if !ok {
		return Foreground{}, unsupportedForeground("node process %d runs an unsupported script %q", leader.pid, script)
	}

	children := make([]foregroundProcess, 0, 1)
	for _, process := range members {
		if process.ppid != leader.pid || process.pgid != group {
			continue
		}
		childHarness, childOK := HarnessForExecutable(process.comm)
		if childOK && childHarness == harness {
			children = append(children, process)
		}
	}
	if len(children) > 1 {
		return Foreground{}, fmt.Errorf("node process %d has multiple same-harness native children", leader.pid)
	}
	pids := []int{leader.pid}
	if len(children) == 1 {
		pids = append(pids, children[0].pid)
	}
	return Foreground{Harness: harness, PIDs: pids}, nil
}

func unsupportedForeground(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnsupportedForeground, fmt.Sprintf(format, args...))
}

func parseForegroundPS(table string) (map[int]foregroundProcess, error) {
	processes := make(map[int]foregroundProcess)
	for _, line := range strings.Split(table, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			return nil, fmt.Errorf("invalid ps process row: %q", line)
		}
		pid, pidErr := strconv.Atoi(fields[0])
		ppid, ppidErr := strconv.Atoi(fields[1])
		pgid, pgidErr := strconv.Atoi(fields[2])
		tpgid, tpgidErr := strconv.Atoi(fields[3])
		comm := strings.TrimSpace(strings.Join(fields[4:], " "))
		if pidErr != nil || ppidErr != nil || pgidErr != nil || tpgidErr != nil || pid <= 0 || comm == "" {
			return nil, fmt.Errorf("invalid ps process row: %q", line)
		}
		if _, exists := processes[pid]; exists {
			return nil, fmt.Errorf("invalid ps process row: duplicate PID %d", pid)
		}
		processes[pid] = foregroundProcess{pid: pid, ppid: ppid, pgid: pgid, tpgid: tpgid, comm: comm}
	}
	if len(processes) == 0 {
		return nil, fmt.Errorf("invalid ps process table: no process rows")
	}
	return processes, nil
}

// paneProcessTree returns only rows reachable from panePID through PPID. The
// map's values are copied so callers cannot accidentally select an unrelated
// process with the same process group.
func paneProcessTree(panePID int, processes map[int]foregroundProcess) map[int]foregroundProcess {
	children := make(map[int][]int)
	for _, process := range processes {
		children[process.ppid] = append(children[process.ppid], process.pid)
	}
	for parent := range children {
		sort.Ints(children[parent])
	}
	members := make(map[int]foregroundProcess)
	pending := []int{panePID}
	for len(pending) > 0 {
		pid := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if _, seen := members[pid]; seen {
			continue
		}
		process, ok := processes[pid]
		if !ok {
			continue
		}
		members[pid] = process
		pending = append(pending, children[pid]...)
	}
	return members
}

func isNodeExecutable(command string) bool {
	return filepath.Base(strings.TrimSpace(command)) == "node"
}

// nodeScriptArgument extracts Node's positional script argument rather than
// searching all arguments for a package name. In particular, --eval/--print
// code that happens to mention a supported path is not a script wrapper.
func nodeScriptArgument(commandLine string) (string, bool) {
	tokens, ok := splitCommandLine(commandLine)
	if !ok || len(tokens) < 2 || filepath.Base(tokens[0]) != "node" {
		return "", false
	}

	for i := 1; i < len(tokens); i++ {
		token := tokens[i]
		if token == "--" {
			if i+1 >= len(tokens) || tokens[i+1] == "" {
				return "", false
			}
			return tokens[i+1], true
		}
		if !strings.HasPrefix(token, "-") || token == "-" {
			return token, token != ""
		}
		name, _, _ := strings.Cut(token, "=")
		if nodeOptionTerminatesScript(name) || strings.HasPrefix(token, "-e") && !strings.HasPrefix(token, "--") || strings.HasPrefix(token, "-p") && !strings.HasPrefix(token, "--") {
			return "", false
		}
		if name != token {
			if nodeOptionConsumesArgument(name) {
				continue
			}
			return "", false
		}
		if nodeOptionConsumesArgument(token) {
			if i+1 >= len(tokens) {
				return "", false
			}
			i++
			continue
		}
		switch token {
		case "--no-warnings", "--trace-warnings", "--enable-source-maps", "--no-deprecation", "--trace-deprecation", "--inspect":
			continue
		default:
			return "", false
		}
	}
	return "", false
}

func nodeOptionTerminatesScript(option string) bool {
	switch option {
	case "-e", "--eval", "-p", "--print", "-c", "--check", "-i", "--interactive", "-h", "--help", "-v", "--version":
		return true
	default:
		return false
	}
}

func nodeOptionConsumesArgument(option string) bool {
	switch option {
	case "-r", "--require", "--import", "--loader", "--experimental-loader", "--conditions", "--input-type", "--env-file", "--openssl-config", "--icu-data-dir", "--redirect-warnings", "--title", "--stack-trace-limit", "--inspect-port", "--inspect-brk", "--inspect-publish-uid":
		return true
	default:
		return false
	}
}

func splitCommandLine(input string) ([]string, bool) {
	var tokens []string
	var token strings.Builder
	quote := rune(0)
	escaped := false
	inToken := false
	flush := func() {
		if inToken {
			tokens = append(tokens, token.String())
			token.Reset()
			inToken = false
		}
	}
	for _, character := range input {
		if escaped {
			token.WriteRune(character)
			inToken = true
			escaped = false
			continue
		}
		if quote != 0 {
			if character == quote {
				quote = 0
				inToken = true
			} else if character == '\\' && quote == '"' {
				escaped = true
			} else {
				token.WriteRune(character)
				inToken = true
			}
			continue
		}
		switch {
		case character == '\\':
			escaped = true
			inToken = true
		case character == '\'' || character == '"':
			quote = character
			inToken = true
		case unicode.IsSpace(character):
			flush()
		default:
			token.WriteRune(character)
			inToken = true
		}
	}
	if escaped || quote != 0 {
		return nil, false
	}
	flush()
	return tokens, true
}
