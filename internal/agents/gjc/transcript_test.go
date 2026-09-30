package gjc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func gjcHeader(id string, version int) map[string]any {
	return map[string]any{"type": "session", "version": version, "id": id}
}

func gjcMessage(id string, parent any, role, text string) map[string]any {
	return map[string]any{
		"type":     "message",
		"id":       id,
		"parentId": parent,
		"message": map[string]any{
			"role":    role,
			"content": []any{map[string]any{"type": "text", "text": text}},
		},
	}
}

const wantGJCContextClearedMarker = "> Context cleared — subsequent messages started without the previous context."

func gjcContextClear(id string, parent any) map[string]any {
	return map[string]any{"type": "custom", "id": id, "parentId": parent, "customType": "context_clear"}
}

func writeGJCRecords(t *testing.T, path string, records []map[string]any) {
	t.Helper()
	var contents strings.Builder
	for _, record := range records {
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatalf("marshal record: %v", err)
		}
		contents.Write(encoded)
		contents.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(contents.String()), 0o600); err != nil {
		t.Fatalf("write records: %v", err)
	}
}

func TestRenderGJCFilteringAndMarkdown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	user := gjcMessage("u", nil, "user", "# Question\n\nKeep this Markdown")
	assistant := gjcMessage("a", "u", "assistant", "```go\nfmt.Println(1)\n```")
	assistantMessage := assistant["message"].(map[string]any)
	assistantMessage["stopReason"] = "aborted"
	assistantMessage["content"] = []any{
		map[string]any{"type": "text", "text": "```go\nfmt.Println(1)\n```"},
		map[string]any{"type": "thinking", "thinking": "PRIVATE THOUGHT"},
		map[string]any{"type": "toolCall", "name": "shell", "arguments": "SECRET COMMAND"},
	}
	tool := gjcMessage("tool", "a", "toolResult", "PRIVATE TOOL RESULT")
	metadata := map[string]any{"type": "custom", "id": "meta", "parentId": "tool", "data": map[string]any{"text": "HIDDEN"}}
	injected := gjcMessage("injected", "meta", "user", "INTERNAL INSTRUCTION")
	injected["message"].(map[string]any)["attribution"] = "agent"
	repeated := gjcMessage("u2", "injected", "user", "# Question\n\nKeep this Markdown")
	repeated["message"].(map[string]any)["content"] = []any{
		map[string]any{"type": "image", "data": "SECRET IMAGE"},
		map[string]any{"type": "text", "text": "# Question\n\nKeep this Markdown"},
	}
	final := gjcMessage("final", "u2", "assistant", "Public final")
	writeGJCRecords(t, path, []map[string]any{
		gjcHeader("session-main", 5), user, assistant, tool, metadata, injected, repeated, final,
	})

	document, err := Render(path)
	if err != nil {
		t.Fatalf("RenderGJC: %v", err)
	}
	if document.Harness != "gjc" || document.SessionID != "session-main" {
		t.Fatalf("unexpected document identity: %#v", document)
	}
	for _, expected := range []string{
		"# GJC transcript",
		"# Question\n\nKeep this Markdown",
		"```go\nfmt.Println(1)\n```",
		"[Image attachment omitted]",
		"## GJC (interrupted)",
		"Public final",
	} {
		if !strings.Contains(document.Markdown, expected) {
			t.Errorf("Markdown missing %q:\n%s", expected, document.Markdown)
		}
	}
	if got := strings.Count(document.Markdown, "# Question\n\nKeep this Markdown"); got != 2 {
		t.Errorf("repeated user message count = %d, want 2", got)
	}
	for _, excluded := range []string{"PRIVATE TOOL RESULT", "PRIVATE THOUGHT", "SECRET COMMAND", "SECRET IMAGE", "INTERNAL INSTRUCTION", "HIDDEN"} {
		if strings.Contains(document.Markdown, excluded) {
			t.Errorf("Markdown leaked %q:\n%s", excluded, document.Markdown)
		}
	}
}

func TestRenderGJCAmbiguousBranchesSelectLatestPersistedChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	header := gjcHeader("session-main", 5)
	user := gjcMessage("u", nil, "user", "Question")
	answer := gjcMessage("a", "u", "assistant", "Primary answer")
	alternate := gjcMessage("other", "u", "assistant", "Other branch")
	latest := gjcMessage("latest", "a", "assistant", "Latest answer")
	writeGJCRecords(t, path, []map[string]any{header, user, answer, alternate, latest})

	document, err := Render(path)
	if err != nil {
		t.Fatalf("latest branch rejected: %v", err)
	}
	for _, expected := range []string{"Question", "Primary answer", "Latest answer"} {
		if !strings.Contains(document.Markdown, expected) {
			t.Errorf("latest chain omitted %q:\n%s", expected, document.Markdown)
		}
	}
	if strings.Contains(document.Markdown, "Other branch") {
		t.Fatalf("abandoned sibling branch rendered:\n%s", document.Markdown)
	}
}

func TestRenderGJCContextClearBridgesSegmentsAndExcludesSiblings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	patch := func(values map[string]any) map[string]any {
		return map[string]any{"type": "header_patch", "patch": values}
	}
	records := []map[string]any{
		gjcHeader("session-main", 5),
		gjcMessage("u1", nil, "user", "Segment one question"),
		gjcMessage("sibling1", "u1", "assistant", "Abandoned sibling one"),
		gjcMessage("a1", "u1", "assistant", "Segment one answer"),
		patch(map[string]any{"title": "before clear one"}),
		gjcContextClear("clear1", nil),
		patch(map[string]any{"title": "after clear one"}),
		gjcMessage("u2", "clear1", "user", "Segment two question"),
		gjcMessage("sibling2", "u2", "assistant", "Abandoned sibling two"),
		gjcMessage("a2", "u2", "assistant", "Segment two answer"),
		patch(map[string]any{"title": "before clear two"}),
		gjcContextClear("clear2", nil),
		patch(map[string]any{"title": "after clear two"}),
		gjcMessage("u3", "clear2", "user", "Segment three question"),
		gjcMessage("sibling3", "u3", "assistant", "Abandoned sibling three"),
		gjcMessage("a3", "u3", "assistant", "Segment three answer"),
		patch(map[string]any{"title": "trailing metadata"}),
	}
	writeGJCRecords(t, path, records)

	document, err := Render(path)
	if err != nil {
		t.Fatalf("context-clear history rejected: %v", err)
	}
	ordered := []string{
		"Segment one question",
		"Segment one answer",
		wantGJCContextClearedMarker,
		"Segment two question",
		"Segment two answer",
		wantGJCContextClearedMarker,
		"Segment three question",
		"Segment three answer",
	}
	searchOffset := 0
	for _, expected := range ordered {
		relative := strings.Index(document.Markdown[searchOffset:], expected)
		if relative < 0 {
			t.Fatalf("content %q missing or out of order in:\n%s", expected, document.Markdown)
		}
		index := searchOffset + relative
		searchOffset = index + len(expected)
	}
	if got := strings.Count(document.Markdown, wantGJCContextClearedMarker); got != 2 {
		t.Fatalf("context-clear marker count = %d, want 2:\n%s", got, document.Markdown)
	}
	for _, excluded := range []string{"Abandoned sibling one", "Abandoned sibling two", "Abandoned sibling three", "before clear one", "after clear one", "trailing metadata"} {
		if strings.Contains(document.Markdown, excluded) {
			t.Fatalf("unexpected metadata or sibling %q in:\n%s", excluded, document.Markdown)
		}
	}
}

func TestRenderGJCContextClearEdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		records     []map[string]any
		want        []string
		excluded    []string
		markerCount int
		wantError   bool
	}{
		{
			name: "clear after public history without new messages",
			records: []map[string]any{
				gjcHeader("main", 5),
				gjcMessage("u", nil, "user", "Before clear"),
				gjcMessage("a", "u", "assistant", "Last answer"),
				gjcContextClear("clear", nil),
			},
			want:        []string{"Before clear", "Last answer"},
			markerCount: 1,
		},
		{
			name: "consecutive clears",
			records: []map[string]any{
				gjcHeader("main", 5),
				gjcMessage("u", nil, "user", "Before clears"),
				gjcMessage("a", "u", "assistant", "Answer before clears"),
				gjcContextClear("clear1", nil),
				gjcContextClear("clear2", nil),
			},
			want:        []string{"Before clears", "Answer before clears"},
			markerCount: 2,
		},
		{
			name: "ordinary disconnected root terminates",
			records: []map[string]any{
				gjcHeader("main", 5),
				gjcMessage("old", nil, "user", "Old segment"),
				gjcMessage("old-answer", "old", "assistant", "Old answer"),
				{"type": "custom", "id": "root", "parentId": nil, "customType": "other"},
				gjcMessage("new", "root", "user", "New segment"),
				gjcMessage("new-answer", "new", "assistant", "New answer"),
			},
			want:     []string{"New segment", "New answer"},
			excluded: []string{"Old segment", "Old answer"},
		},
		{
			name: "clear-only",
			records: []map[string]any{
				gjcHeader("main", 5),
				gjcContextClear("clear", nil),
			},
			wantError: true,
		},
		{
			name:      "header-only",
			records:   []map[string]any{{"type": "session", "version": 5, "id": "main"}},
			wantError: true,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.jsonl")
			writeGJCRecords(t, path, testCase.records)
			document, err := Render(path)
			if testCase.wantError {
				if err == nil {
					t.Fatalf("expected no-public-conversation error")
				}
				return
			}
			if err != nil {
				t.Fatalf("RenderGJC: %v", err)
			}
			for _, expected := range testCase.want {
				if !strings.Contains(document.Markdown, expected) {
					t.Errorf("missing %q in:\n%s", expected, document.Markdown)
				}
			}
			for _, excluded := range testCase.excluded {
				if strings.Contains(document.Markdown, excluded) {
					t.Errorf("unexpected %q in:\n%s", excluded, document.Markdown)
				}
			}
			if got := strings.Count(document.Markdown, wantGJCContextClearedMarker); got != testCase.markerCount {
				t.Errorf("context-clear marker count = %d, want %d", got, testCase.markerCount)
			}

		})
	}
}

func TestRenderGJCMessageCustomTypeContextClearIsNotBridged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	ordinaryRoot := gjcMessage("ordinary-root", nil, "user", "New disconnected root")
	ordinaryRoot["customType"] = "context_clear"
	writeGJCRecords(t, path, []map[string]any{
		gjcHeader("main", 5),
		gjcMessage("old", nil, "user", "Old history"),
		gjcMessage("old-answer", "old", "assistant", "Old answer"),
		ordinaryRoot,
	})

	document, err := Render(path)
	if err != nil {
		t.Fatalf("RenderGJC: %v", err)
	}
	if !strings.Contains(document.Markdown, "New disconnected root") {
		t.Fatalf("ordinary root missing:\n%s", document.Markdown)
	}
	for _, excluded := range []string{"Old history", "Old answer", wantGJCContextClearedMarker} {
		if strings.Contains(document.Markdown, excluded) {
			t.Fatalf("ordinary message customType was bridged or marked (%q):\n%s", excluded, document.Markdown)
		}
	}
}

func TestRenderGJCEmptyFileErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeGJCRecords(t, path, nil)
	if _, err := Render(path); err == nil {
		t.Fatal("empty GJC file rendered without an error")
	}
}

func TestRenderGJCRejectsMalformedSessionGraphs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	cases := []struct {
		name    string
		records []map[string]any
	}{
		{
			name: "duplicate id",
			records: []map[string]any{
				gjcHeader("session-main", 5),
				gjcMessage("u", nil, "user", "one"),
				gjcMessage("u", nil, "user", "two"),
			},
		},
		{
			name: "missing parent",
			records: []map[string]any{
				gjcHeader("session-main", 5),
				gjcMessage("a", "missing", "assistant", "bad"),
			},
		},
		{
			name: "cycle",
			records: []map[string]any{
				gjcHeader("session-main", 5),
				gjcMessage("a", "a", "assistant", "cycle"),
			},
		},
		{
			name: "unsupported version",
			records: []map[string]any{
				gjcHeader("session-main", 4),
			},
		},
		{
			name: "invalid content",
			records: []map[string]any{
				gjcHeader("session-main", 5),
				{
					"type": "message", "id": "u", "parentId": nil,
					"message": map[string]any{"role": "user", "content": "not an array"},
				},
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			writeGJCRecords(t, path, testCase.records)
			if _, err := Render(path); err == nil {
				t.Fatalf("RenderGJC unexpectedly succeeded")
			}
		})
	}
}

func TestRenderGJCHandlesPartialTailAndCorruptCompleteRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	records := []map[string]any{
		gjcHeader("session-main", 5),
		gjcMessage("u", nil, "user", "Question"),
		gjcMessage("a", "u", "assistant", "Answer"),
	}
	writeGJCRecords(t, path, records)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	if _, err := file.WriteString(`{"unfinished":`); err != nil {
		t.Fatalf("append partial record: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close session: %v", err)
	}
	document, err := Render(path)
	if err != nil {
		t.Fatalf("partial tail: %v", err)
	}
	if !strings.Contains(document.Markdown, "unfinished final log record") {
		t.Fatalf("partial note missing:\n%s", document.Markdown)
	}
	if !strings.Contains(document.Markdown, "Answer") {
		t.Fatalf("last complete graph record missing:\n%s", document.Markdown)
	}

	if err := os.WriteFile(path, []byte("{\"type\":\"session\",\"version\":5,\"id\":\"session-main\"}\nnot-json\n"), 0o600); err != nil {
		t.Fatalf("write corrupt session: %v", err)
	}
	if _, err := Render(path); err == nil || !strings.Contains(err.Error(), "Invalid GJC JSONL") {
		t.Fatalf("corrupt complete record error = %v", err)
	}
}

func TestGJCHeaderPatchesDoNotBecomeGraphEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeGJCRecords(t, path, []map[string]any{
		gjcHeader("main", 5),
		gjcMessage("u", nil, "user", "Question"),
		{"type": "header_patch", "patch": map[string]any{"starred": true}},
		{"type": "header_patch", "patch": map[string]any{"title": "PRIVATE TITLE", "titleSource": "auto"}},
		gjcMessage("a", "u", "assistant", "Answer"),
		{"type": "header_patch", "patch": map[string]any{"starred": false}},
	})
	doc, err := Render(path)
	if err != nil {
		t.Fatal(err)
	}
	if doc.SessionID != "main" || !strings.Contains(doc.Markdown, "Question") || !strings.Contains(doc.Markdown, "Answer") || strings.Contains(doc.Markdown, "PRIVATE TITLE") {
		t.Fatal("header patch altered public transcript")
	}
}

func TestGJCRejectsMalformedHeaderPatches(t *testing.T) {
	for _, patch := range []map[string]any{
		{"type": "header_patch"},
		{"type": "header_patch", "patch": "bad"},
		{"type": "header_patch", "patch": map[string]any{}, "id": "a"},
		{"type": "header_patch", "patch": map[string]any{}, "parentId": "u"},
	} {
		path := filepath.Join(t.TempDir(), "session.jsonl")
		writeGJCRecords(t, path, []map[string]any{gjcHeader("main", 5), gjcMessage("u", nil, "user", "Question"), patch})
		if _, err := Render(path); err == nil || !strings.Contains(err.Error(), "header_patch") {
			t.Fatalf("malformed patch accepted: %v", err)
		}
	}
}
