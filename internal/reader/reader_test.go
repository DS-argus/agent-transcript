package reader

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestReadersUseNativeInteractiveCommandsWithoutPagerDependencies(t *testing.T) {
	home := t.TempDir()
	env := []string{
		"HOME=" + home,
		"PAGER=custom-pager",
		"GLOW_STYLE=dark",
		"GLOW_PAGER=true",
	}
	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			var dependencies []string
			command, err := prepare(name, env, func(binary string) (string, error) {
				dependencies = append(dependencies, binary)
				return "/bin/" + binary, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(dependencies, []string{name}) {
				t.Fatalf("looked up unexpected dependencies: %v", dependencies)
			}
			if command.BottomKey != "G" {
				t.Fatalf("default bottom key = %q, want G", command.BottomKey)
			}
			wantArgs := []string{name}
			wantEnv := append([]string(nil), env...)
			if name == "glow" {
				wantArgs = []string{"glow", "--tui", "-"}
				wantEnv = []string{
					"HOME=" + home,
					"PAGER=custom-pager",
					"GLOW_STYLE=dark",
					"GLOW_PAGER=false",
				}
			}
			if !reflect.DeepEqual(command.Args, wantArgs) {
				t.Fatalf("args = %v, want %v", command.Args, wantArgs)
			}
			if !reflect.DeepEqual(command.Env, wantEnv) {
				t.Fatalf("env = %v, want %v", command.Env, wantEnv)
			}
		})
	}
}

func TestRejectsUnsupportedReadersAndMissingReaders(t *testing.T) {
	for _, name := range []string{"mdcat", "mdt"} {
		t.Run(name, func(t *testing.T) {
			lookedUp := false
			_, err := prepare(name, nil, func(binary string) (string, error) {
				lookedUp = true
				return "/bin/" + binary, nil
			})
			if err == nil || !strings.Contains(err.Error(), "unsupported reader") {
				t.Fatalf("unsupported reader error = %v", err)
			}
			if lookedUp {
				t.Fatal("looked up unsupported reader")
			}
		})
	}

	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			_, err := prepare(name, nil, func(binary string) (string, error) {
				return "", fmt.Errorf("missing %s", binary)
			})
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("missing reader error = %v", err)
			}
		})
	}
}

func TestGlowOverridePreservesOriginalEnvironment(t *testing.T) {
	env := []string{
		"HOME=/home/user",
		"GLOW_STYLE=dark",
		"GLOW_PAGER=true",
		"PAGER=custom-pager",
	}
	before := append([]string(nil), env...)
	command, err := prepare("glow", env, func(binary string) (string, error) {
		if binary != "glow" {
			t.Fatalf("unexpected lookup %s", binary)
		}
		return "/bin/glow", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(env, before) {
		t.Fatalf("input env mutated: %v", env)
	}
	count := 0
	var pager string
	for _, value := range command.Env {
		if strings.HasPrefix(value, "GLOW_PAGER=") {
			count++
			pager = strings.TrimPrefix(value, "GLOW_PAGER=")
		}
	}
	if count != 1 {
		t.Fatalf("GLOW_PAGER entries = %d, want 1", count)
	}
	if pager != "false" {
		t.Fatalf("GLOW_PAGER = %q, want false", pager)
	}
}
