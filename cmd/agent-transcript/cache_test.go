package main

import (
	"os"
	"path/filepath"
	"testing"
)

func isolatedCache(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	base, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(base, "agent-transcript")
}
func TestRuntimeArtifactsSharePrivateCacheRoot(t *testing.T) {
	root := isolatedCache(t)
	for _, kind := range []string{"locks", "snapshots"} {
		path, err := cacheDirectory(kind)
		if err != nil {
			t.Fatal(err)
		}
		if path != filepath.Join(root, kind) {
			t.Fatal("wrong runtime path", path)
		}
		for _, dir := range []string{root, path} {
			info, err := os.Stat(dir)
			if err != nil || info.Mode().Perm() != 0700 {
				t.Fatalf("nonprivate directory %s: %v", dir, err)
			}
		}
	}
	source := writeJSON(t, filepath.Join(t.TempDir(), "session.jsonl"), map[string]any{"type": "session", "version": 5, "id": "main"}, map[string]any{"type": "message", "id": "a", "parentId": nil, "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "Cache test"}}}})
	snapshot, err := writeSnapshot(snapshotIdentity{path: source, harness: "gjc", session: "main"})
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.cleanup()
	if filepath.Dir(snapshot.directory) != filepath.Join(root, "snapshots") {
		t.Fatal("snapshot outside unified root", snapshot.directory)
	}
	info, err := os.Stat(snapshot.path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("snapshot not private", err)
	}
	snapshot.cleanup()
	if _, err := os.Stat(snapshot.directory); !os.IsNotExist(err) {
		t.Fatal("snapshot directory remains", err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatal("source log changed", err)
	}
}
func TestCacheRejectsSymlinkAndInvalidCategory(t *testing.T) {
	root := isolatedCache(t)
	external := t.TempDir()
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, "snapshots")); err != nil {
		t.Fatal(err)
	}
	if _, err := cacheDirectory("snapshots"); err == nil {
		t.Fatal("symlink accepted")
	}
	if _, err := cacheDirectory("../escape"); err == nil {
		t.Fatal("invalid category accepted")
	}
}
