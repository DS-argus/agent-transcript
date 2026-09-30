package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUniqueDeduplicatesCanonicalPathsWithoutUsingTimestamps(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.jsonl")
	b := filepath.Join(dir, "b.jsonl")
	if err := os.WriteFile(a, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "alias.jsonl")
	if err := os.Symlink(a, link); err != nil {
		t.Fatal(err)
	}
	got, err := Unique("claude", []Source{{"claude", a}, {"claude", link}, {"claude", a}})
	if err != nil || got != (Source{"claude", a}) {
		t.Fatalf("duplicate selection: %+v %v", got, err)
	}
	for _, paths := range [][]Source{{{"claude", a}, {"claude", b}}, {{"claude", b}, {"claude", a}}} {
		if _, err := Unique("claude", paths); err == nil || !strings.Contains(err.Error(), "expected exactly one") {
			t.Fatalf("ambiguity accepted: %v", err)
		}
	}
	if _, err := Unique("claude", nil); err == nil {
		t.Fatal("missing candidate accepted")
	}
	if _, err := Unique("claude", []Source{{"claude", filepath.Join(dir, "missing")}}); err == nil {
		t.Fatal("missing file accepted")
	}
}
func TestValidatePIDsRejectsUnboundedAndInvalidOwners(t *testing.T) {
	for _, pids := range [][]int{nil, {}, {0}, {-1}, {1, 0}} {
		if err := ValidatePIDs("claude", pids); err == nil {
			t.Fatalf("invalid owners accepted: %v", pids)
		}
	}
	if err := ValidatePIDs("claude", []int{1, 2}); err != nil {
		t.Fatal(err)
	}
}
func TestValidIDRejectsPathSelectors(t *testing.T) {
	for _, id := range []string{"", "../session", "a/b", "a_b", "x.jsonl", "a b", "a\x00b"} {
		if ValidID(id) {
			t.Fatalf("unsafe selector accepted: %q", id)
		}
	}
	for _, id := range []string{"main", "01a0-bafe-1A", "aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa"} {
		if !ValidID(id) {
			t.Fatalf("safe selector rejected: %q", id)
		}
	}
}
