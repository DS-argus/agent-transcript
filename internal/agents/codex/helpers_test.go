package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeRecords(t *testing.T, path string, records ...map[string]any) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	for _, record := range records {
		if err := json.NewEncoder(file).Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func resolver(t *testing.T, files ...string) Resolver {
	t.Helper()
	return Resolver{Run: func(name string, args ...string) ([]byte, error) {
		if name != "lsof" || !reflect.DeepEqual(args, []string{"-a", "-p", "100,101", "-Fn"}) {
			t.Fatalf("unexpected process inspection: %s %v", name, args)
		}
		var lines []string
		for _, path := range files {
			lines = append(lines, "n"+path)
		}
		return []byte(strings.Join(lines, "\n")), nil
	}}
}
