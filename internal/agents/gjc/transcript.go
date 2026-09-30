package gjc

import (
	"fmt"
	"strings"

	"agent-transcript/internal/transcript"
)

const gjcContextClearedMarker = "> Context cleared — subsequent messages started without the previous context."

// Render renders the public conversation on the latest persisted GJC graph
// record's parent chain.
//
// GJC session files contain a session header followed by an append-only graph
// of records. Header patches are presentation metadata, not graph nodes. A
// parentless context_clear record is bridged to the immediately preceding valid
// graph record for this historical view; this does not prove the previous
// in-memory active leaf or mutate the persisted parentId. Other parentless
// records terminate traversal.
func Render(path string) (transcript.Document, error) {
	records, partial, err := transcript.ReadJSONL(path)
	if err != nil {
		return transcript.Document{}, fmt.Errorf("Invalid GJC JSONL: %w", err)
	}
	if len(records) == 0 {
		return transcript.Document{}, fmt.Errorf("Empty GJC session file.")
	}

	header := records[0]
	version, versionOK := header["version"].(float64)
	sessionID := transcript.String(header, "id")
	if transcript.String(header, "type") != "session" || !versionOK || version != 5 || sessionID == "" {
		return transcript.Document{}, fmt.Errorf("Expected a GJC version 5 session file.")
	}

	entries := make(map[string]map[string]any, len(records)-1)
	contextClearBridges := make(map[string]string)
	lastGraphID := ""
	for _, record := range records[1:] {
		// Header patches update session presentation metadata, not the message
		// graph: they intentionally have no entry ID or parent link.
		if transcript.String(record, "type") == "header_patch" {
			if transcript.Object(record, "patch") == nil {
				return transcript.Document{}, fmt.Errorf("Invalid GJC header_patch: expected patch object.")
			}
			if _, exists := record["id"]; exists {
				return transcript.Document{}, fmt.Errorf("Invalid GJC header_patch: unexpected graph ID.")
			}
			if _, exists := record["parentId"]; exists {
				return transcript.Document{}, fmt.Errorf("Invalid GJC header_patch: unexpected graph parent.")
			}
			continue
		}
		ident := transcript.String(record, "id")
		if ident == "" {
			return transcript.Document{}, fmt.Errorf("Invalid or duplicate GJC entry ID.")
		}
		if _, exists := entries[ident]; exists {
			return transcript.Document{}, fmt.Errorf("Invalid or duplicate GJC entry ID.")
		}

		parentValue, hasParent := record["parentId"]
		if hasParent && parentValue != nil {
			parent, parentOK := parentValue.(string)
			if !parentOK || parent == "" {
				return transcript.Document{}, fmt.Errorf("Missing or out-of-order GJC parent for %s.", ident)
			}
			if _, exists := entries[parent]; !exists {
				return transcript.Document{}, fmt.Errorf("Missing or out-of-order GJC parent for %s.", ident)
			}
		}

		// Context-clear roots deliberately bridge to file order rather than
		// claiming to recover an unavailable previous active leaf.
		if transcript.String(record, "type") == "custom" && transcript.String(record, "customType") == "context_clear" && (!hasParent || parentValue == nil) && lastGraphID != "" {
			contextClearBridges[ident] = lastGraphID
		}
		entries[ident] = record
		lastGraphID = ident
	}

	branch := make([]map[string]any, 0, len(entries))
	visited := make(map[string]struct{}, len(entries))
	selected := lastGraphID
	for selected != "" {
		if _, seen := visited[selected]; seen {
			return transcript.Document{}, fmt.Errorf("Invalid cyclic GJC parent graph at %s.", selected)
		}
		entry, exists := entries[selected]
		if !exists {
			return transcript.Document{}, fmt.Errorf("Missing GJC ancestor for %s.", selected)
		}
		visited[selected] = struct{}{}
		branch = append(branch, entry)

		parentValue, hasParent := entry["parentId"]
		if hasParent && parentValue != nil {
			parent, parentOK := parentValue.(string)
			if !parentOK || parent == "" {
				return transcript.Document{}, fmt.Errorf("Missing or invalid GJC parent for %s.", selected)
			}
			selected = parent
			continue
		}
		selected = contextClearBridges[selected]
	}

	messages := make([]string, 0, len(branch))
	publicMessageCount := 0
	for index := len(branch) - 1; index >= 0; index-- {
		entry := branch[index]
		if transcript.String(entry, "type") == "custom" && transcript.String(entry, "customType") == "context_clear" {
			messages = append(messages, gjcContextClearedMarker)
		}
		if transcript.String(entry, "type") != "message" {
			continue
		}
		message := transcript.Object(entry, "message")
		if message == nil {
			return transcript.Document{}, fmt.Errorf("Invalid GJC message.")
		}
		role := transcript.String(message, "role")
		if role != "user" && role != "assistant" {
			continue
		}
		if role == "user" {
			if attribution, present := message["attribution"]; present && attribution != nil {
				value, ok := attribution.(string)
				if !ok || value != "user" {
					continue
				}
			}
		}

		contentValue, present := message["content"]
		if !present {
			contentValue = []any{}
		}
		content, contentOK := contentValue.([]any)
		if !contentOK {
			return transcript.Document{}, fmt.Errorf("Unsupported GJC message content (expected content blocks).")
		}
		parts := make([]string, 0, len(content))
		for _, rawBlock := range content {
			block, blockOK := rawBlock.(map[string]any)
			if !blockOK {
				continue
			}
			switch transcript.String(block, "type") {
			case "text":
				if text, ok := block["text"].(string); ok {
					parts = append(parts, text)
				}
			case "image":
				if role == "user" {
					parts = append(parts, "[Image attachment omitted]")
				}
			}
		}
		text := strings.TrimSpace(strings.Join(parts, "\n\n"))
		if text == "" {
			continue
		}

		label := "User"
		if role == "assistant" {
			label = "GJC"
			if stopReason := transcript.String(message, "stopReason"); stopReason == "error" || stopReason == "aborted" {
				label += " (interrupted)"
			}
		}
		messages = append(messages, fmt.Sprintf("## %s\n\n%s", label, text))
		publicMessageCount++
	}

	// Markers provide context only; a clear/header-only graph still has no
	// public conversation and must fail like any other empty transcript.
	if publicMessageCount == 0 {
		return transcript.Document{}, fmt.Errorf("no public conversation messages in gjc session")
	}
	return transcript.NewDocument("GJC", sessionID, messages, partial)
}
