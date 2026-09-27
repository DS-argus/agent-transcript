package transcript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeClaudeFixture(t *testing.T, records []map[string]any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			file.Close()
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func claudeFixtureRecord(typ, id, parent string, message map[string]any) map[string]any {
	record := map[string]any{
		"type":        typ,
		"uuid":        id,
		"sessionId":   "session-1",
		"isSidechain": false,
	}
	if parent != "" {
		record["parentUuid"] = parent
	}
	if message != nil {
		record["message"] = message
	}
	return record
}

func claudeUserMessage(content any) map[string]any {
	return map[string]any{"role": "user", "content": content}
}

func claudeAssistantMessage(id string, content []any) map[string]any {
	return map[string]any{"role": "assistant", "id": id, "content": content}
}

func claudeTextBlock(text string) map[string]any {
	return map[string]any{"type": "text", "text": text}
}

func TestRenderClaudeCurrentSchema(t *testing.T) {
	path := writeClaudeFixture(t, []map[string]any{
		claudeFixtureRecord("user", "u1", "", claudeUserMessage("Question **one**")),
		claudeFixtureRecord("assistant", "a1", "u1", claudeAssistantMessage("same-message-id", []any{
			map[string]any{"type": "thinking", "thinking": "private reasoning"},
			claudeTextBlock("First answer with `code`."),
			map[string]any{"type": "tool_use", "id": "tool-1", "name": "secret-tool", "input": map[string]any{"secret": "private"}},
		})),
		claudeFixtureRecord("system", "s1", "a1", nil),
		claudeFixtureRecord("user", "u2", "s1", claudeUserMessage([]any{
			claudeTextBlock("Second question\n\nwith Markdown."),
			map[string]any{"type": "image", "source": map[string]any{"type": "base64", "data": "private-image"}},
			map[string]any{"type": "tool_result", "tool_use_id": "tool-1", "content": "private tool output"},
		})),
		claudeFixtureRecord("user", "u3", "u2", claudeUserMessage([]any{
			map[string]any{"type": "tool_result", "tool_use_id": "tool-2", "content": "another private result"},
		})),
		claudeFixtureRecord("assistant", "a2", "u3", claudeAssistantMessage("same-message-id", []any{
			map[string]any{"type": "thinking", "thinking": "more private reasoning"},
			claudeTextBlock("Second answer."),
		})),
		func() map[string]any {
			record := claudeFixtureRecord("user", "side-u", "u1", claudeUserMessage("sidechain secret"))
			record["isSidechain"] = true
			return record
		}(),
		func() map[string]any {
			record := claudeFixtureRecord("user", "meta-u", "u1", claudeUserMessage("injected secret"))
			record["isMeta"] = true
			return record
		}(),
	})

	document, err := RenderClaude(path)
	if err != nil {
		t.Fatal(err)
	}
	if document.Harness != "claude" || document.SessionID != "session-1" {
		t.Fatalf("unexpected document identity: %#v", document)
	}
	for _, expected := range []string{
		"## User\n\nQuestion **one**",
		"## Claude\n\nFirst answer with `code`.",
		"Second question\n\nwith Markdown.",
		"[Image attachment omitted]",
		"## Claude\n\nSecond answer.",
	} {
		if !strings.Contains(document.Markdown, expected) {
			t.Errorf("missing %q in rendered document", expected)
		}
	}
	for _, excluded := range []string{"private reasoning", "private tool output", "sidechain secret", "injected secret"} {
		if strings.Contains(document.Markdown, excluded) {
			t.Errorf("private text %q leaked into rendered document", excluded)
		}
	}
	if strings.Count(document.Markdown, "same-message-id") != 0 {
		t.Error("message IDs should not be rendered")
	}
	if strings.Count(document.Markdown, "## Claude") != 2 {
		t.Errorf("expected both assistant blocks, got %d", strings.Count(document.Markdown, "## Claude"))
	}
}

func TestRenderClaudeAmbiguousBranchesFailClosed(t *testing.T) {
	records := []map[string]any{
		claudeFixtureRecord("user", "root", "", claudeUserMessage("Question")),
		claudeFixtureRecord("assistant", "left", "root", claudeAssistantMessage("left-id", []any{claudeTextBlock("Left answer")})),
		claudeFixtureRecord("assistant", "right", "root", claudeAssistantMessage("right-id", []any{claudeTextBlock("Right answer")})),
	}
	path := writeClaudeFixture(t, records)
	_, err := RenderClaude(path)
	if err == nil {
		t.Fatal("ambiguous public branches rendered without an error")
	}
	if strings.Contains(err.Error(), "--leaf-id") {
		t.Fatalf("ambiguity error exposes removed manual selection advice: %v", err)
	}
	for _, candidate := range []string{"left", "right"} {
		if !strings.Contains(err.Error(), candidate) {
			t.Errorf("ambiguity error omitted candidate tip %q: %v", candidate, err)
		}
	}
}

func TestRenderClaudePartialTailAndUnknownSchema(t *testing.T) {
	path := writeClaudeFixture(t, []map[string]any{
		claudeFixtureRecord("user", "u1", "", claudeUserMessage("Question")),
		claudeFixtureRecord("assistant", "a1", "u1", claudeAssistantMessage("a1", []any{claudeTextBlock("Answer")})),
	})
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"type":"assistant"`); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	document, err := RenderClaude(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(document.Markdown, "unfinished final log record") {
		t.Error("partial final record was not reported")
	}

	file, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("\n"); err != nil {
		file.Close()
		t.Fatal(err)
	}
	file.Close()
	if _, err := RenderClaude(path); err == nil || !strings.Contains(err.Error(), "invalid JSONL") {
		t.Fatalf("expected corrupt complete record error, got %v", err)
	}

	unknown := writeClaudeFixture(t, []map[string]any{
		claudeFixtureRecord("user", "u1", "", claudeUserMessage("Question")),
		{"type": "future-claude-record", "sessionId": "session-1", "message": map[string]any{"role": "user", "content": map[string]any{"future": true}}},
	})
	if _, err := RenderClaude(unknown); err == nil || !strings.Contains(err.Error(), "unsupported Claude record type") {
		t.Fatalf("expected unknown schema error, got %v", err)
	}
}

func TestRenderClaudeMissingAncestryFailsClosed(t *testing.T) {
	path := writeClaudeFixture(t, []map[string]any{
		claudeFixtureRecord("user", "u1", "evicted-parent", claudeUserMessage("Question")),
	})
	if _, err := RenderClaude(path); err == nil || !strings.Contains(err.Error(), "missing ancestry") {
		t.Fatalf("expected missing ancestry error, got %v", err)
	}
}
