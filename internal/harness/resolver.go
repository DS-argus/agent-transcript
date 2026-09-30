// Package harness identifies the foreground agent and dispatches to its adapter.
package harness

import (
	"agent-transcript/internal/agents"
	"agent-transcript/internal/agents/claude"
	"agent-transcript/internal/agents/gjc"
	"agent-transcript/internal/process"
)

type Resolver struct {
	GJCDir    string
	ClaudeDir string
	Run       process.Runner
	Identity  func(int) (string, error)
}

func (r Resolver) configured() Resolver {
	if r.Run == nil {
		r.Run = process.Run
	}
	if r.Identity == nil {
		r.Identity = process.ProcessIdentity
	}
	if r.GJCDir == "" {
		r.GJCDir = gjc.AgentDir()
	}
	if r.ClaudeDir == "" {
		r.ClaudeDir = claude.ConfigDir()
	}
	return r
}

// Resolve reads only the foreground owner's selected agent state.
func (r Resolver) Resolve(panePID int) (agents.Source, error) {
	r = r.configured()
	foreground, err := IdentifyForeground(panePID, r.Run)
	if err != nil {
		return agents.Source{}, err
	}
	return r.Locate(foreground.Harness, foreground.PIDs)
}

func (r Resolver) Locate(name string, pids []int) (agents.Source, error) {
	r = r.configured()
	adapter, ok := Lookup(name)
	if !ok {
		return agents.Source{}, unsupportedForeground("unsupported harness: %s", name)
	}
	if err := agents.ValidatePIDs(name, pids); err != nil {
		return agents.Source{}, err
	}
	return adapter.Locate(r, pids)
}
