package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// Runtime artifacts share one OS user-cache root. Build artifacts and reader
// configuration remain owned by their respective tools.
func cacheDirectory(kind string) (string, error) {
	if kind != "snapshots" && kind != "locks" {
		return "", fmt.Errorf("unknown cache category: %s", kind)
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	root := filepath.Join(base, "agent-transcript")
	for _, path := range []string{root, filepath.Join(root, kind)} {
		if err := os.MkdirAll(path, 0700); err != nil {
			return "", err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("cache path is not a real directory: %s", path)
		}
		if err := os.Chmod(path, 0700); err != nil {
			return "", err
		}
	}
	return filepath.Join(root, kind), nil
}
