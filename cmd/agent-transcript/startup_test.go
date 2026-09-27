package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocumentAnchorSurvivesFormattingAndWrapping(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.md")
	if err := os.WriteFile(path, []byte("\n---\n# GJC transcript\n\nLater body\n"), 0600); err != nil {
		t.Fatal(err)
	}
	anchor, err := documentAnchor(path)
	if err != nil || anchor != "gjctranscript" {
		t.Fatalf("%q %v", anchor, err)
	}
	screen := "│ GJC trans │\n│ cript │"
	if normalizeVisible(screen) != anchor {
		t.Fatal("wrapped heading was not recognized")
	}
	if normalizeVisible("# 한글 문서") != "한글문서" {
		t.Fatal("Unicode heading normalization failed")
	}
}

func TestDocumentAnchorErrorsAndEmptySnapshot(t *testing.T) {
	dir := t.TempDir()
	if _, err := documentAnchor(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing snapshot accepted")
	}
	path := filepath.Join(dir, "empty.md")
	if err := os.WriteFile(path, []byte("\n---\n"), 0600); err != nil {
		t.Fatal(err)
	}
	anchor, err := documentAnchor(path)
	if err != nil || anchor != "" {
		t.Fatalf("empty snapshot: %q %v", anchor, err)
	}
}

func TestLongFirstLineUsesOnlyAVisiblePrefix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "long.md")
	if err := os.WriteFile(path, []byte(strings.Repeat("한", 5000)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	anchor, err := documentAnchor(path)
	if err != nil || len([]rune(anchor)) != 32 {
		t.Fatalf("anchor length=%d err=%v", len([]rune(anchor)), err)
	}
}
