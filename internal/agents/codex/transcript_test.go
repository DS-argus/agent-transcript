package codex

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func codexMeta(session string) map[string]any {
	return map[string]any{
		"type": "session_meta",
		"payload": map[string]any{
			"id":     session,
			"source": "cli",
		},
	}
}

func codexMessage(role, text, id, thread, turn string) map[string]any {
	kind := "text"
	if role == "AgentMessage" {
		kind = "Text"
	}
	return codexMessageParts(role, id, thread, turn, []any{
		map[string]any{"type": kind, "text": text},
	})
}

func codexMessageParts(role, id, thread, turn string, content []any) map[string]any {
	return map[string]any{
		"type": "event_msg",
		"payload": map[string]any{
			"type":      "item_completed",
			"thread_id": thread,
			"turn_id":   turn,
			"item": map[string]any{
				"type":    role,
				"id":      id,
				"content": content,
			},
		},
	}
}

func writeCodexRecords(t *testing.T, records ...map[string]any) string {
	t.Helper()
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			t.Fatalf("encode record: %v", err)
		}
	}
	path := filepath.Join(t.TempDir(), "rollout-main.jsonl")
	if err := os.WriteFile(path, output.Bytes(), 0600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	return path
}

func TestRenderFiltersPrivateAndOtherThreadRecords(t *testing.T) {
	answer := codexMessage("AgentMessage", "## Heading\n\n```python\nprint(1)\n```", "a1", "main", "t1")
	records := []map[string]any{
		codexMeta("main"),
		codexMessage("UserMessage", "한글 질문", "u1", "main", "t1"),
		answer,
		answer,
		codexMessage("Reasoning", "PRIVATE REASONING", "r1", "main", "t1"),
		codexMessage("CommandExecution", "TOOL LOG", "tool", "main", "t1"),
		codexMessage("AgentMessage", "OTHER THREAD", "a2", "worker", "t1"),
		{"type": "response_item", "payload": map[string]any{
			"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "INJECTED CONTEXT"}},
		}},
		codexMessage("AgentMessage", answer["payload"].(map[string]any)["item"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string), "a3", "main", "t2"),
	}

	document, err := Render(writeCodexRecords(t, records...))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if document.Harness != "codex" {
		t.Fatalf("Harness = %q, want codex", document.Harness)
	}
	if document.SessionID != "main" {
		t.Fatalf("SessionID = %q, want main", document.SessionID)
	}
	for _, included := range []string{"한글 질문", "## Heading", "```python", "print(1)"} {
		if !strings.Contains(document.Markdown, included) {
			t.Errorf("Markdown does not contain %q", included)
		}
	}
	if got := strings.Count(document.Markdown, "print(1)"); got != 2 {
		t.Errorf("print(1) count = %d, want 2 (duplicate ID removed, distinct ID retained)", got)
	}
	for _, excluded := range []string{"PRIVATE REASONING", "TOOL LOG", "OTHER THREAD", "INJECTED CONTEXT"} {
		if strings.Contains(document.Markdown, excluded) {
			t.Errorf("Markdown contains excluded text %q", excluded)
		}
	}
	if !strings.HasPrefix(document.Markdown, "# Codex transcript\n\nSession: `main`") {
		t.Errorf("Markdown is missing shared document framing: %q", document.Markdown[:min(len(document.Markdown), 80)])
	}
}

func TestRenderPreservesMarkdownAndOmitsImageData(t *testing.T) {
	content := []any{
		map[string]any{"type": "text", "text": "before"},
		map[string]any{"type": "Text", "text": "## original heading\n\n- item"},
		map[string]any{"type": "image", "image_url": "https://secret.example/image.png"},
		map[string]any{"type": "local_image", "path": "/secret/local.png"},
		map[string]any{"type": "unknown", "text": "not public"},
		"not an object",
	}
	path := writeCodexRecords(t, codexMeta("main"), codexMessageParts("UserMessage", "u1", "main", "t1", content))
	document, err := Render(path)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, included := range []string{"before", "## original heading", "- item", codexImagePlaceholder} {
		if !strings.Contains(document.Markdown, included) {
			t.Errorf("Markdown does not contain %q", included)
		}
	}
	for _, secret := range []string{"https://secret.example/image.png", "/secret/local.png", "not public"} {
		if strings.Contains(document.Markdown, secret) {
			t.Errorf("Markdown contains omitted content %q", secret)
		}
	}
}

func TestRenderDeduplicatesOnlyByTurnAndID(t *testing.T) {
	records := []map[string]any{
		codexMeta("main"),
		codexMessage("UserMessage", "same text", "m1", "main", "t1"),
		codexMessage("UserMessage", "same text", "m1", "main", "t1"),
		codexMessage("UserMessage", "same text", "m2", "main", "t1"),
		codexMessage("UserMessage", "same text", "m1", "main", "t2"),
		codexMessageParts("UserMessage", "", "main", "t3", []any{map[string]any{"type": "text", "text": "no ID"}}),
		codexMessageParts("UserMessage", "", "main", "t3", []any{map[string]any{"type": "text", "text": "no ID"}}),
	}
	document, err := Render(writeCodexRecords(t, records...))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got := strings.Count(document.Markdown, "same text"); got != 3 {
		t.Errorf("same text count = %d, want 3", got)
	}
	if got := strings.Count(document.Markdown, "no ID"); got != 2 {
		t.Errorf("no ID count = %d, want 2", got)
	}
}

func TestRenderThreadSelection(t *testing.T) {
	missingThread := codexMessage("UserMessage", "missing thread", "m0", "main", "t0")
	delete(missingThread["payload"].(map[string]any), "thread_id")
	numericThread := codexMessage("UserMessage", "numeric thread", "m3", "main", "t3")
	numericThread["payload"].(map[string]any)["thread_id"] = float64(7)
	records := []map[string]any{
		codexMeta("main"),
		missingThread,
		codexMessage("UserMessage", "matching thread", "m1", "main", "t1"),
		codexMessage("UserMessage", "other thread", "m2", "worker", "t2"),
		numericThread,
	}
	// A missing or null thread ID is accepted by the renderer, matching Codex's
	// records emitted before thread attribution is attached.
	nullThread := codexMessage("UserMessage", "null thread", "m4", "main", "t4")
	nullThread["payload"].(map[string]any)["thread_id"] = nil
	records = append(records, nullThread)
	document, err := Render(writeCodexRecords(t, records...))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, included := range []string{"missing thread", "matching thread", "null thread"} {
		if !strings.Contains(document.Markdown, included) {
			t.Errorf("Markdown does not contain %q", included)
		}
	}
	for _, excluded := range []string{"other thread", "numeric thread"} {
		if strings.Contains(document.Markdown, excluded) {
			t.Errorf("Markdown contains excluded text %q", excluded)
		}
	}
}

func TestRenderRejectsMalformedMessageContent(t *testing.T) {
	for _, malformed := range []any{nil, map[string]any{"type": "text"}, "text"} {
		t.Run("malformed", func(t *testing.T) {
			item := map[string]any{"type": "UserMessage", "id": "bad", "content": malformed}
			record := map[string]any{"type": "event_msg", "payload": map[string]any{
				"type": "item_completed", "thread_id": "main", "turn_id": "t1", "item": item,
			}}
			_, err := Render(writeCodexRecords(t, codexMeta("main"), record))
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), "message content") {
				t.Fatalf("Render error = %v, want malformed message content error", err)
			}
		})
	}
}

func TestRenderPartialTailIsReportedAndCompleteCorruptionFails(t *testing.T) {
	path := writeCodexRecords(t, codexMeta("main"), codexMessage("UserMessage", "question", "u1", "main", "t1"))
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatalf("open rollout: %v", err)
	}
	if _, err := file.WriteString(`{"type":"event_msg","payload":`); err != nil {
		file.Close()
		t.Fatalf("append partial record: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close rollout: %v", err)
	}

	document, err := Render(path)
	if err != nil {
		t.Fatalf("Render partial tail: %v", err)
	}
	if !strings.Contains(document.Markdown, "unfinished final log record was omitted") {
		t.Errorf("Markdown does not report omitted partial tail")
	}
	if !strings.Contains(document.Markdown, "question") {
		t.Errorf("Markdown lost complete message before partial tail")
	}

	file, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatalf("reopen rollout: %v", err)
	}
	if _, err := file.WriteString("\n"); err != nil {
		file.Close()
		t.Fatalf("complete malformed record: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close rollout: %v", err)
	}
	if _, err := Render(path); err == nil || !strings.Contains(strings.ToLower(err.Error()), "invalid codex jsonl") {
		t.Fatalf("Render error = %v, want invalid Codex JSONL error", err)
	}
}

func TestRenderHandlesLargeRecords(t *testing.T) {
	text := "prefix\n" + strings.Repeat("x", 128*1024) + "\nsuffix"
	document, err := Render(writeCodexRecords(t, codexMeta("main"), codexMessage("AgentMessage", text, "a1", "main", "t1")))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(document.Markdown, text) {
		t.Fatal("large Markdown message was not preserved")
	}
}

func TestRenderRejectsUnsupportedFormatsAndMissingSessionID(t *testing.T) {
	tests := []struct {
		name    string
		records []map[string]any
		wantErr string
	}{
		{name: "empty", records: nil, wantErr: "not a codex rollout"},
		{name: "gjc", records: []map[string]any{{"type": "session", "id": "branch"}}, wantErr: "not a codex rollout"},
		{name: "response item", records: []map[string]any{codexMeta("main"), {"type": "response_item"}}, wantErr: "no saved public messages"},
		{name: "missing ID", records: []map[string]any{{"type": "session_meta", "payload": map[string]any{}}}, wantErr: "session id"},
		{name: "non-string ID", records: []map[string]any{{"type": "session_meta", "payload": map[string]any{"id": 7}}}, wantErr: "session id"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writeCodexRecords(t, test.records...)
			_, err := Render(path)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), test.wantErr) {
				t.Fatalf("Render error = %v, want %q", err, test.wantErr)
			}
		})
	}

	rawPath := filepath.Join(t.TempDir(), "terminal.log")
	if err := os.WriteFile(rawPath, []byte("$ codex\nprivate terminal output\n"), 0600); err != nil {
		t.Fatalf("write raw transcript: %v", err)
	}
	if _, err := Render(rawPath); err == nil {
		t.Fatal("Render accepted raw terminal output")
	}
}

func TestRenderEmptyForkNotice(t *testing.T) {
	meta := codexMeta("fork")
	meta["payload"].(map[string]any)["forked_from_id"] = "parent"
	settings := map[string]any{"type": "event_msg", "payload": map[string]any{"type": "thread_settings_applied", "thread_id": "fork"}}
	path := writeCodexRecords(t, meta, settings)
	_, err := Render(path)
	if err == nil || !strings.Contains(err.Error(), "No saved public messages") || !strings.Contains(err.Error(), "try again") {
		t.Fatalf("missing actionable empty-session notice: %v", err)
	}
	path = writeCodexRecords(t, meta, settings, codexMessage("UserMessage", "First fork message", "u", "fork", "t"))
	doc, err := Render(path)
	if err != nil || !strings.Contains(doc.Markdown, "First fork message") {
		t.Fatalf("first saved fork message not rendered: %v", err)
	}
}
