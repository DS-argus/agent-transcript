// Package agents defines shared discovery results, without importing agent implementations.
package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Source struct {
	Harness string
	Path    string
}

func ValidatePIDs(name string, pids []int) error {
	if len(pids) == 0 {
		return fmt.Errorf("no owner PID for %s", name)
	}
	for _, pid := range pids {
		if pid <= 0 {
			return fmt.Errorf("invalid owner PID for %s", name)
		}
	}
	return nil
}

// Unique rejects ambiguous identities; ordering and file timestamps are not selection criteria.
func Unique(name string, candidates []Source) (Source, error) {
	var unique []Source
	seen := map[string]bool{}
	for _, source := range candidates {
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
		var paths []string
		for _, source := range unique {
			paths = append(paths, source.Path)
		}
		suffix := ""
		if len(paths) > 0 {
			suffix = "\n" + strings.Join(paths, "\n")
		}
		return Source{}, fmt.Errorf("identified %s foreground TUI, but found %d sessions; expected exactly one%s", name, len(unique), suffix)
	}
	return unique[0], nil
}

func ExpandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}

func ValidID(id string) bool {
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
