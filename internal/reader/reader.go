// Package reader connects rendered transcripts to Markdown viewers, independently
// of agent discovery and transcript parsing.
package reader

import (
	"fmt"
	"os/exec"
	"strings"
)

const Default = "leaf"

// Command is an argv-based invocation; reader selection is never a shell command.
type Command struct {
	Executable string
	Args       []string
	Env        []string
	BottomKey  string
}

func Names() []string { return []string{"leaf", "glow"} }
func Supported(name string) bool {
	for _, supported := range Names() {
		if name == supported {
			return true
		}
	}
	return false
}

func Prepare(name string, env []string) (Command, error) {
	return prepare(name, env, exec.LookPath)
}

func prepare(name string, env []string, lookup func(string) (string, error)) (Command, error) {
	if !Supported(name) {
		return Command{}, fmt.Errorf("unsupported reader %q (choose %s)", name, strings.Join(Names(), ", "))
	}
	path, err := lookup(name)
	if err != nil {
		return Command{}, fmt.Errorf("reader %s is not installed on PATH: %w", name, err)
	}
	command := Command{
		Executable: path,
		Args:       []string{name},
		Env:        append([]string(nil), env...),
		BottomKey:  "G",
	}
	switch name {
	case "leaf": // leaf < snapshot.md
		// Native Markdown TUI, with the user's configuration left intact.
	case "glow": // GLOW_PAGER=false glow --tui - < snapshot.md
		// A positional file alone renders and exits. Explicit TUI mode keeps
		// the document navigable and avoids Glow's external-pager mode.
		command.Args = append(command.Args, "--tui", "-")
		// Glow 3 treats even --pager=false as requesting its external pager.
		// Override only the UI mode through Viper's environment setting instead.
		command.Env = overrideEnvironment(command.Env, "GLOW_PAGER=false")
	}
	return command, nil
}

// syscall.Exec does not deduplicate environment entries like exec.Cmd does.
// Remove old values instead of appending duplicates that readers may read first.
func overrideEnvironment(env []string, values ...string) []string {
	result := append([]string(nil), env...)
	for _, value := range values {
		key, _, _ := strings.Cut(value, "=")
		prefix := key + "="
		filtered := make([]string, 0, len(result)+1)
		for _, existing := range result {
			if !strings.HasPrefix(existing, prefix) {
				filtered = append(filtered, existing)
			}
		}
		result = append(filtered, value)
	}
	return result
}
