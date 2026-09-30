package process

import (
	"errors"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

func OpenFiles(pids []int, run Runner) ([]string, error) {
	ids := make([]string, len(pids))
	for i, pid := range pids {
		ids[i] = strconv.Itoa(pid)
	}
	data, err := run("lsof", "-a", "-p", strings.Join(ids, ","), "-Fn")
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return nil, err
		}
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "n/") && strings.HasSuffix(line, ".jsonl") {
			seen[line[1:]] = true
		}
	}
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}
