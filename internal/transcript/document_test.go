package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadJSONLLargeAndPartialRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.jsonl")
	content := "{\"text\":\"" + strings.Repeat("x", 300000) + "\"}\n{\"unfinished\":"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	records, partial, err := ReadJSONL(path)
	if err != nil || !partial || len(records) != 1 || len(String(records[0], "text")) != 300000 {
		t.Fatalf("records=%d partial=%v err=%v", len(records), partial, err)
	}
	if err := os.WriteFile(path, []byte(content+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadJSONL(path); err == nil {
		t.Fatal("complete corrupt record silently ignored")
	}
}
func TestReadJSONLFinalValidAndNonObject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.jsonl")
	for _, value := range []string{"null\n", "[]\n", "123\n"} {
		os.WriteFile(path, []byte(value), 0600)
		if _, _, err := ReadJSONL(path); err == nil {
			t.Fatal("non-object accepted", value)
		}
	}
	os.WriteFile(path, []byte("{\"type\":\"user\"}"), 0600)
	rows, partial, err := ReadJSONL(path)
	if err != nil || partial || len(rows) != 1 {
		t.Fatalf("valid final record lost: %v %v", partial, err)
	}
}
