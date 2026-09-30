// Package process supplies bounded OS process inspection for agent discovery.
package process

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type Runner func(string, ...string) ([]byte, error)

func Run(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	// Claude PID registries use UTC/English process birth times.
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
