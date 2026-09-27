// Package transcript renders public harness messages as Markdown.
package transcript

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

type Document struct {
	Harness   string
	SessionID string
	Markdown  string
}

// ReadJSONL reads only the bytes present at open time. Incomplete final records
// are reported separately; corrupt complete records fail without hiding data.
func ReadJSONL(path string) ([]map[string]any, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, false, err
	}
	// read only snapshot
	reader := bufio.NewReader(io.LimitReader(file, info.Size()))
	var records []map[string]any
	for line := 1; ; line++ {
		raw, readErr := reader.ReadBytes('\n')
		if len(raw) == 0 && readErr == io.EOF {
			break
		}
		if readErr != nil && readErr != io.EOF {
			return nil, false, readErr
		}
		var record map[string]any
		if err := json.Unmarshal(raw, &record); err != nil {
			if readErr == io.EOF && !json.Valid(raw) {
				return records, true, nil
			}
			return nil, false, fmt.Errorf("invalid JSONL at %s:%d: %w", path, line, err)
		}
		if record == nil {
			return nil, false, fmt.Errorf("invalid JSONL object at %s:%d", path, line)
		}
		records = append(records, record)
		if readErr == io.EOF {
			break
		}
	}
	return records, false, nil
}

func String(record map[string]any, key string) string { value, _ := record[key].(string); return value }
func Object(record map[string]any, key string) map[string]any {
	value, _ := record[key].(map[string]any)
	return value
}
func Array(record map[string]any, key string) []any { value, _ := record[key].([]any); return value }
func Bool(record map[string]any, key string) bool   { value, _ := record[key].(bool); return value }

// NewDocument shares output framing, not harness-specific message interpretation.
func NewDocument(harness, sessionID string, messages []string, partial bool) (Document, error) {
	if len(messages) == 0 {
		return Document{}, fmt.Errorf("no public conversation messages in %s session", harness)
	}
	note := ""
	if partial {
		note = "\n\nAn unfinished final log record was omitted."
	}
	// Intro
	markdown := fmt.Sprintf("# %s transcript\n\nSession: `%s`\n\nPersisted messages only; tool output and thinking are omitted.%s\n\n---\n\n%s\n", harness, sessionID, note, strings.Join(messages, "\n\n---\n\n"))
	return Document{Harness: strings.ToLower(harness), SessionID: sessionID, Markdown: markdown}, nil
}
