// Package harness identifies the foreground agent before locating its session.
package harness

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Source struct{ Harness, Path string }
type Resolver struct {
	GJCDir    string
	ClaudeDir string
	Run       func(string, ...string) ([]byte, error)
	Identity  func(int) (string, error)
}

func Run(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	// Claude registry process birth times use UTC/English. Do not mutate the
	// interactive terminal or reader environment.
	cmd.Env = append(os.Environ(), "TZ=UTC", "LC_ALL=C")
	output, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return output, fmt.Errorf("%s: %s: %w", name, strings.TrimSpace(string(exit.Stderr)), err)
		}
	}
	return output, err
}
func (r Resolver) configured() Resolver {
	if r.Run == nil {
		r.Run = Run
	}
	if r.Identity == nil {
		r.Identity = ProcessIdentity
	}
	if r.GJCDir == "" {
		r.GJCDir = GJCAgentDir()
	}
	if r.ClaudeDir == "" {
		r.ClaudeDir = ClaudeConfigDir()
	}
	return r
}

// Resolve identifies the foreground agent before inspecting its session files.
func (r Resolver) Resolve(panePID int) (Source, error) {
	r = r.configured()
	foreground, err := IdentifyForeground(panePID, r.Run)
	if err != nil {
		return Source{}, err
	}
	return r.Locate(foreground.Harness, foreground.PIDs)
}

// Locate consumes an already selected agent and its bounded owner PID set.
func (r Resolver) Locate(name string, pids []int) (Source, error) {
	r = r.configured()
	adapter, ok := Lookup(name)
	if !ok {
		return Source{}, fmt.Errorf("unsupported harness: %s", name)
	}
	if len(pids) == 0 {
		return Source{}, fmt.Errorf("no owner PID for %s", name)
	}
	for _, pid := range pids {
		if pid <= 0 {
			return Source{}, fmt.Errorf("invalid owner PID for %s", name)
		}
	}
	matches, err := adapter.Locate(r, pids)
	if err != nil {
		return Source{}, err
	}
	unique := make([]Source, 0, len(matches))
	seen := map[string]bool{}
	for _, source := range matches {
		canonical, err := filepath.EvalSymlinks(source.Path)
		if err != nil {
			return Source{}, err
		}
		canonical, err = filepath.Abs(canonical)
		if err != nil {
			return Source{}, err
		}
		key := source.Harness + "\x00" + canonical
		if !seen[key] {
			seen[key] = true
			unique = append(unique, source)
		}
	}
	if len(unique) != 1 {
		details := []string{}
		for _, source := range unique {
			details = append(details, source.Path)
		}
		suffix := ""
		if len(details) > 0 {
			suffix = "\n" + strings.Join(details, "\n")
		}
		return Source{}, fmt.Errorf("identified %s foreground TUI, but found %d sessions; expected exactly one%s", name, len(unique), suffix)
	}
	return unique[0], nil
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}
func validID(id string) bool {
	if id == "" {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}
func number(m map[string]any, key string) float64 { v, _ := m[key].(float64); return v }
