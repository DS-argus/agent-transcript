package harness

import (
	"fmt"
	"path/filepath"
	"strings"

	"agent-transcript/internal/transcript"
)

// Adapter binds the four harness-specific contracts in one registration. The
// CLI and foreground selector do not maintain their own lists of agent names.
type Adapter struct {
	Name        string
	Executables []string
	NodeScripts []string
	Locate      func(Resolver, []int) ([]Source, error)
	Detect      func([]map[string]any) (string, bool)
	Render      func(string) (transcript.Document, error)
}

var adapters = []Adapter{
	{Name: "codex", Executables: []string{"codex"}, NodeScripts: []string{"/@openai/codex/bin/codex.js"}, Locate: Resolver.locateCodex,
		Detect: recognizeCodex,
		Render: transcript.RenderCodex},
	{Name: "gjc", Executables: []string{"gjc"}, Locate: Resolver.gjcSessions,
		Detect: recognizeGJC,
		Render: transcript.RenderGJC},
}

func Names() []string {
	names := make([]string, 0, len(adapters))
	for _, a := range adapters {
		names = append(names, a.Name)
	}
	return names
}
func Lookup(name string) (Adapter, bool) {
	for _, a := range adapters {
		if a.Name == name {
			return a, true
		}
	}
	return Adapter{}, false
}
func HarnessForExecutable(command string) (string, bool) {
	base := filepath.Base(strings.TrimSpace(command))
	for _, a := range adapters {
		for _, name := range a.Executables {
			if base == name {
				return a.Name, true
			}
		}
	}
	return "", false
}
func HarnessForNodeScript(script string) (string, bool) {
	if canonical, err := filepath.EvalSymlinks(script); err == nil {
		script = canonical
	}
	script = filepath.ToSlash(filepath.Clean(script))
	for _, a := range adapters {
		for _, suffix := range a.NodeScripts {
			if strings.HasSuffix(script, suffix) {
				return a.Name, true
			}
		}
	}
	return "", false
}

func DetectFile(path string) (string, string, error) {
	records, _, err := transcript.ReadJSONL(path)
	if err != nil {
		return "", "", err
	}
	for _, a := range adapters {
		if id, ok := a.Detect(records); ok {
			if id == "" {
				return "", "", fmt.Errorf("%s session ID missing: %s", a.Name, path)
			}
			return a.Name, id, nil
		}
	}
	return "", "", fmt.Errorf("unrecognized transcript format: %s", path)
}

func RenderFile(name, path string) (transcript.Document, error) {
	adapter, ok := Lookup(name)
	if !ok {
		return transcript.Document{}, fmt.Errorf("unsupported harness: %s", name)
	}
	return adapter.Render(path)
}
