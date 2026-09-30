package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeClaudeFixture(t *testing.T, records []map[string]any) string {
	t.Helper()
	return writeRecords(t, filepath.Join(t.TempDir(), "claude.jsonl"), records...)
}

func claudeFixtureRecord(typ, id, parent string, message map[string]any) map[string]any {
	record := map[string]any{"type": typ, "uuid": id, "sessionId": "session-1", "isSidechain": false, "parentUuid": nil}
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
func claudeTextBlock(text string) map[string]any { return map[string]any{"type": "text", "text": text} }

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
		claudeFixtureRecord("user", "u3", "u2", claudeUserMessage([]any{map[string]any{"type": "tool_result", "tool_use_id": "tool-2", "content": "another private result"}})),
		claudeFixtureRecord("assistant", "a2", "u3", claudeAssistantMessage("same-message-id", []any{
			map[string]any{"type": "thinking", "thinking": "more private reasoning"}, claudeTextBlock("Second answer."),
		})),
		func() map[string]any {
			r := claudeFixtureRecord("user", "side-u", "u1", claudeUserMessage("sidechain secret"))
			r["isSidechain"] = true
			return r
		}(),
		func() map[string]any {
			r := claudeFixtureRecord("user", "meta-u", "u1", claudeUserMessage("injected secret"))
			r["isMeta"] = true
			return r
		}(),
	})
	document, err := Render(path)
	if err != nil {
		t.Fatal(err)
	}
	if document.Harness != "claude" || document.SessionID != "session-1" {
		t.Fatalf("unexpected identity: %#v", document)
	}
	for _, expected := range []string{"## User\n\nQuestion **one**", "## Claude\n\nFirst answer with `code`.", "Second question\n\nwith Markdown.", "## Claude\n\nSecond answer."} {
		if !strings.Contains(document.Markdown, expected) {
			t.Errorf("missing %q", expected)
		}
	}
	for _, excluded := range []string{"private reasoning", "private tool output", "private-image", "sidechain secret", "injected secret", "same-message-id"} {
		if strings.Contains(document.Markdown, excluded) {
			t.Errorf("private text %q leaked", excluded)
		}
	}
	if strings.Count(document.Markdown, "## Claude") != 2 {
		t.Fatalf("lost assistant blocks: %s", document.Markdown)
	}
}

func TestRenderClaudeBranchesSelectLatestPersistedPrimaryLeaf(t *testing.T) {
	path := writeClaudeFixture(t, []map[string]any{
		claudeFixtureRecord("user", "root", "", claudeUserMessage("Question")),
		claudeFixtureRecord("assistant", "left", "root", claudeAssistantMessage("left-id", []any{claudeTextBlock("Abandoned answer")})),
		claudeFixtureRecord("user", "old-followup", "left", claudeUserMessage("Abandoned followup")),
		claudeFixtureRecord("assistant", "right", "root", claudeAssistantMessage("right-id", []any{claudeTextBlock("Current answer")})),
		claudeFixtureRecord("attachment", "terminal", "right", nil),
		claudeFixtureRecord("system", "duration", "terminal", nil),
	})
	document, err := Render(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(document.Markdown, "Current answer") || strings.Contains(document.Markdown, "Abandoned") {
		t.Fatalf("wrong persisted branch: %s", document.Markdown)
	}
}

func TestRenderClaudeCanonicalIdentityIgnoresPriorWireAlias(t *testing.T) {
	for _, mode := range []string{"clear", "fork", "resume"} {
		t.Run(mode, func(t *testing.T) {
			user := claudeFixtureRecord("user", "u", "", claudeUserMessage("Question"))
			attachment := claudeFixtureRecord("attachment", "attachment", "u", nil)
			answer := claudeFixtureRecord("assistant", "a", "attachment", claudeAssistantMessage("message", []any{claudeTextBlock("Answer")}))
			for _, record := range []map[string]any{user, attachment, answer} {
				record["session_id"] = "prior-session"
			}
			path := writeClaudeFixture(t, []map[string]any{user, attachment, answer})
			document, err := Render(path)
			if err != nil || document.SessionID != "session-1" || !strings.Contains(document.Markdown, "Answer") {
				t.Fatalf("%+v %v", document, err)
			}
		})
	}
}

func TestRenderClaudeCompactionResetsHistory(t *testing.T) {
	boundary := claudeFixtureRecord("system", "boundary", "", nil)
	boundary["subtype"], boundary["logicalParentUuid"] = "compact_boundary", "old-answer"
	summary := claudeFixtureRecord("user", "summary", "boundary", claudeUserMessage("Compact summary **text**"))
	summary["isCompactSummary"], summary["isVisibleInTranscriptOnly"] = true, true
	path := writeClaudeFixture(t, []map[string]any{
		claudeFixtureRecord("user", "old", "", claudeUserMessage("Old disconnected history")),
		claudeFixtureRecord("assistant", "old-answer", "old", claudeAssistantMessage("old-message", []any{claudeTextBlock("Old disconnected answer")})),
		boundary, summary,
		claudeFixtureRecord("user", "new", "summary", claudeUserMessage("Current question")),
		claudeFixtureRecord("assistant", "new-answer", "new", claudeAssistantMessage("new-message", []any{claudeTextBlock("Current answer")})),
	})
	document, err := Render(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(document.Markdown, "Old disconnected") || !strings.Contains(document.Markdown, "## Context compacted\n\nCompact summary **text**") || !strings.Contains(document.Markdown, "Current answer") {
		t.Fatalf("bad compact chain: %s", document.Markdown)
	}
}

func TestRenderClaudePrivateDescendantsDoNotChooseAnotherAgent(t *testing.T) {
	for _, flag := range []string{"isSidechain", "agentId", "teamName"} {
		t.Run(flag, func(t *testing.T) {
			private := claudeFixtureRecord("user", "private", "root", claudeUserMessage("Private agent question"))
			if flag == "isSidechain" {
				private[flag] = true
			} else {
				private[flag] = "private-agent"
			}
			path := writeClaudeFixture(t, []map[string]any{
				claudeFixtureRecord("user", "root", "", claudeUserMessage("Public question")),
				claudeFixtureRecord("assistant", "public", "root", claudeAssistantMessage("public-message", []any{claudeTextBlock("Public answer")})),
				private, claudeFixtureRecord("attachment", "private-context", "private", nil),
				claudeFixtureRecord("assistant", "child", "private-context", claudeAssistantMessage("child-message", []any{claudeTextBlock("Private agent answer")})),
			})
			document, err := Render(path)
			if err != nil || !strings.Contains(document.Markdown, "Public answer") || strings.Contains(document.Markdown, "Private agent") {
				t.Fatalf("%+v %v", document, err)
			}
		})
	}
}

func TestRenderClaudeLocalCommandsAreNotPublicText(t *testing.T) {
	path := writeClaudeFixture(t, []map[string]any{
		claudeFixtureRecord("user", "command", "", claudeUserMessage("<command-name>/clear</command-name>")),
		claudeFixtureRecord("user", "stdout", "command", claudeUserMessage([]any{claudeTextBlock("<local-command-stdout>Private local output</local-command-stdout>")})),
		claudeFixtureRecord("user", "caveat", "stdout", claudeUserMessage("<local-command-caveat>Private local instructions</local-command-caveat>")),
		claudeFixtureRecord("user", "real", "caveat", claudeUserMessage("Real Markdown: `<command-name>` is a tag.")),
	})
	document, err := Render(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(document.Markdown, "Private local") || strings.Contains(document.Markdown, "/clear") || !strings.Contains(document.Markdown, "Real Markdown:") {
		t.Fatalf("bad command filtering: %s", document.Markdown)
	}
}

func TestRenderClaudeDuplicateUUIDPolicy(t *testing.T) {
	root := claudeFixtureRecord("user", "root", "", claudeUserMessage("Question"))
	answer := claudeFixtureRecord("assistant", "answer", "root", claudeAssistantMessage("message", []any{claudeTextBlock("Answer")}))
	if document, err := Render(writeClaudeFixture(t, []map[string]any{root, answer, answer})); err != nil || strings.Count(document.Markdown, "## Claude") != 1 {
		t.Fatalf("exact replay: %+v %v", document, err)
	}
	for _, key := range []string{"message", "parentUuid", "sessionId", "isSidechain", "timestamp"} {
		t.Run(key, func(t *testing.T) {
			conflict := map[string]any{}
			for k, v := range answer {
				conflict[k] = v
			}
			switch key {
			case "message":
				conflict[key] = claudeAssistantMessage("message", []any{claudeTextBlock("Conflicting answer")})
			case "parentUuid":
				conflict[key] = nil
			case "sessionId":
				conflict[key] = "other-session"
			case "isSidechain":
				conflict[key] = true
			case "timestamp":
				conflict[key] = "different-persistence"
			}
			if _, err := Render(writeClaudeFixture(t, []map[string]any{root, answer, conflict})); err == nil {
				t.Fatal("conflicting duplicate accepted")
			}
		})
	}
}

func TestRenderClaudePartialTailAndUnknownSchema(t *testing.T) {
	path := writeClaudeFixture(t, []map[string]any{
		claudeFixtureRecord("user", "u1", "", claudeUserMessage("Question")),
		claudeFixtureRecord("assistant", "a1", "u1", claudeAssistantMessage("a1", []any{claudeTextBlock("Answer")})),
	})
	appendRaw(t, path, `{"type":"assistant"`)
	document, err := Render(path)
	if err != nil || !strings.Contains(document.Markdown, "unfinished final log record") {
		t.Fatalf("%+v %v", document, err)
	}
	appendRaw(t, path, "\n")
	if _, err := Render(path); err == nil || !strings.Contains(err.Error(), "invalid JSONL") {
		t.Fatalf("corrupt complete line: %v", err)
	}
	unknown := writeClaudeFixture(t, []map[string]any{
		claudeFixtureRecord("user", "u1", "", claudeUserMessage("Question")),
		{"type": "future-claude-record", "sessionId": "session-1", "message": map[string]any{"role": "user", "content": map[string]any{"future": true}}},
	})
	if _, err := Render(unknown); err == nil || !strings.Contains(err.Error(), "unsupported Claude record type") {
		t.Fatal(err)
	}
}

func TestRenderClaudeMalformedRecordsFailClosed(t *testing.T) {
	cases := map[string]map[string]any{
		"missing ancestry": claudeFixtureRecord("user", "u", "missing", claudeUserMessage("Question")),
		"cycle":            claudeFixtureRecord("user", "u", "u", claudeUserMessage("Question")),
		"missing message":  claudeFixtureRecord("user", "u", "", nil),
		"unknown content":  claudeFixtureRecord("user", "u", "", claudeUserMessage([]any{map[string]any{"type": "future-private-block", "secret": "secret"}})),
		"malformed block":  claudeFixtureRecord("user", "u", "", claudeUserMessage([]any{17})),
		"wrong role":       claudeFixtureRecord("user", "u", "", map[string]any{"role": "assistant", "content": "wrong"}),
	}
	for name, record := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Render(writeClaudeFixture(t, []map[string]any{record})); err == nil {
				t.Fatal("unsafe record accepted")
			}
		})
	}
	for _, key := range []string{"sessionId", "session_id", "uuid", "parentUuid", "isSidechain", "isMeta", "agentId", "teamName", "isCompactSummary", "isVisibleInTranscriptOnly"} {
		t.Run(key, func(t *testing.T) {
			record := claudeFixtureRecord("user", "u", "", claudeUserMessage("Question"))
			record[key] = 17
			if _, err := Render(writeClaudeFixture(t, []map[string]any{record})); err == nil {
				t.Fatal("invalid field accepted")
			}
		})
	}
	private := claudeFixtureRecord("user", "private", "", claudeUserMessage([]any{map[string]any{"type": "future-private-block", "text": "secret"}}))
	private["isSidechain"] = true
	if _, err := Render(writeClaudeFixture(t, []map[string]any{private})); err == nil {
		t.Fatal("unknown private content accepted")
	}
}

func TestRenderClaudeLargeRecordAndSafeMetadata(t *testing.T) {
	large := "Large public text " + strings.Repeat("x", 1024*1024+1)
	path := writeClaudeFixture(t, []map[string]any{
		{"type": "future-metadata", "sessionId": "session-1", "value": "ignored"},
		claudeFixtureRecord("user", "u", "", claudeUserMessage(large)),
	})
	document, err := Render(path)
	if err != nil || !strings.Contains(document.Markdown, large) {
		t.Fatalf("large record not accepted: %v", err)
	}
}

func TestDetectClaudeCanonicalID(t *testing.T) {
	record := claudeFixtureRecord("user", "u", "", claudeUserMessage("Question"))
	record["session_id"] = "prior-session"
	if id, ok := Detect([]map[string]any{record}); !ok || id != "session-1" {
		t.Fatalf("%q %v", id, ok)
	}
	other := claudeFixtureRecord("user", "other", "", claudeUserMessage("Other"))
	other["sessionId"] = "other-session"
	if _, ok := Detect([]map[string]any{record, other}); ok {
		t.Fatal("mixed canonical identities detected")
	}
}

func appendRaw(t *testing.T, path, text string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(text); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeRecords(t *testing.T, path string, records ...map[string]any) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
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

func TestRenderClaudeMetaAncestorsDoNotHidePublicContinuation(t *testing.T) {
	for _, flag := range []string{"isMeta", "userType"} {
		t.Run(flag, func(t *testing.T) {
			meta := claudeFixtureRecord("user", "meta", "root", claudeUserMessage("Injected private context"))
			if flag == "isMeta" {
				meta[flag] = true
			} else {
				meta[flag] = "internal"
			}
			path := writeClaudeFixture(t, []map[string]any{
				claudeFixtureRecord("user", "root", "", claudeUserMessage("Public question")), meta,
				claudeFixtureRecord("assistant", "answer", "meta", claudeAssistantMessage("message", []any{claudeTextBlock("Public continuation")})),
			})
			document, err := Render(path)
			if err != nil || strings.Contains(document.Markdown, "Injected private") || !strings.Contains(document.Markdown, "Public continuation") {
				t.Fatalf("%+v %v", document, err)
			}
		})
	}
}
