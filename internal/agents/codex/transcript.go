package codex

import (
	"encoding/json"
	"fmt"
	"strings"

	"agent-transcript/internal/transcript"
)

const codexImagePlaceholder = "[Image attachment omitted]"

// Render renders the completed public messages from a Codex rollout.
//
// Codex emits several record families in the same JSONL stream. Only
// event_msg/item_completed records for UserMessage and AgentMessage items are
// public transcript content; all other records are deliberately ignored.
func Render(path string) (transcript.Document, error) {
	records, partial, err := transcript.ReadJSONL(path)
	if err != nil {
		return transcript.Document{}, fmt.Errorf("invalid Codex JSONL at %s: %w", path, err)
	}
	if len(records) == 0 || transcript.String(records[0], "type") != "session_meta" {
		return transcript.Document{}, fmt.Errorf("not a Codex rollout: %s", path)
	}

	metadata := transcript.Object(records[0], "payload")
	sessionID, ok := metadata["id"].(string)
	if !ok || sessionID == "" {
		return transcript.Document{}, fmt.Errorf("missing Codex session ID: %s", path)
	}

	messages := make([]string, 0)
	seen := make(map[string]struct{})
	for index, record := range records {
		if transcript.String(record, "type") != "event_msg" {
			continue
		}
		event := transcript.Object(record, "payload")
		if transcript.String(event, "type") != "item_completed" {
			continue
		}
		if threadID, present := event["thread_id"]; present && threadID != nil {
			thread, isString := threadID.(string)
			if !isString || thread != sessionID {
				continue
			}
		}

		item := transcript.Object(event, "item")
		role := ""
		switch transcript.String(item, "type") {
		case "UserMessage":
			role = "User"
		case "AgentMessage":
			role = "Codex"
		default:
			continue
		}

		// Codex can emit the same item more than once. IDs are scoped by turn;
		// messages without a nonempty string ID are intentionally not
		// deduplicated.
		var dedupKey string
		deduplicate := false
		if id, present := item["id"].(string); present && id != "" {
			key, marshalErr := json.Marshal([2]any{event["turn_id"], id})
			if marshalErr == nil {
				dedupKey = string(key)
				deduplicate = true
				if _, duplicate := seen[dedupKey]; duplicate {
					continue
				}
			}
		}

		content, err := codexContent(item)
		if err != nil {
			return transcript.Document{}, fmt.Errorf("invalid Codex message content at %s:%d: %w", path, index+1, err)
		}
		parts := make([]string, 0, len(content))
		for _, rawPart := range content {
			part, ok := rawPart.(map[string]any)
			if !ok {
				continue
			}
			switch transcript.String(part, "type") {
			case "text", "Text":
				if text, ok := part["text"].(string); ok {
					parts = append(parts, text)
				}
			case "image", "local_image":
				parts = append(parts, codexImagePlaceholder)
			}
		}
		text := strings.TrimSpace(strings.Join(parts, "\n\n"))
		if text == "" {
			continue
		}
		if deduplicate {
			seen[dedupKey] = struct{}{}
		}
		messages = append(messages, fmt.Sprintf("## %s\n\n%s", role, text))
	}
	if len(messages) == 0 {
		return transcript.Document{}, fmt.Errorf("No saved public messages in this Codex session yet. Complete a conversation turn and try again.")
	}
	return transcript.NewDocument("Codex", sessionID, messages, partial)
}

func codexContent(item map[string]any) ([]any, error) {
	if _, present := item["content"]; !present {
		return nil, nil
	}
	content := transcript.Array(item, "content")
	if content == nil {
		return nil, fmt.Errorf("content must be an array")
	}
	return content, nil
}
