package claude

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"agent-transcript/internal/agents"
	"agent-transcript/internal/transcript"
)

// Render follows the latest persisted primary message leaf within this already
// owned file. File timestamps never select a session. Nonmessage graph records
// connect ancestry; sidechains, team messages and injected context are private.
func Render(path string) (transcript.Document, error) {
	records, partial, err := transcript.ReadJSONL(path)
	if err != nil {
		return transcript.Document{}, err
	}
	if len(records) == 0 {
		return transcript.Document{}, fmt.Errorf("empty Claude session file: %s", path)
	}
	sessionID, err := claudeSessionID(path, records)
	if err != nil {
		return transcript.Document{}, err
	}
	nodes := make(map[string]*claudeNode)
	ordered := make([]*claudeNode, 0, len(records))
	for index, record := range records {
		line := index + 1
		typ, ok := record["type"].(string)
		if !ok || typ == "" {
			return transcript.Document{}, fmt.Errorf("invalid Claude record type at %s:%d", path, line)
		}
		if !claudeKnownRecordType(typ) {
			if claudeHasTranscriptShape(record) {
				return transcript.Document{}, fmt.Errorf("unsupported Claude record type %q at %s:%d", typ, path, line)
			}
			continue
		}
		if !claudeGraphRecordType(typ) {
			if claudeHasTranscriptShape(record) {
				return transcript.Document{}, fmt.Errorf("unsupported Claude metadata transcript shape at %s:%d", path, line)
			}
			continue
		}
		if typ != "user" && typ != "assistant" && !recordHasKey(record, "uuid") {
			if claudeHasTranscriptShape(record) {
				return transcript.Document{}, fmt.Errorf("invalid Claude graph record at %s:%d", path, line)
			}
			continue
		}
		node, err := claudeNodeFor(path, line, record, typ, sessionID)
		if err != nil {
			return transcript.Document{}, err
		}
		// Only an exact replay is safe to coalesce. Changed ancestry, identity,
		// flags or message content sharing a UUID is ambiguous and rejected.
		if previous, exists := nodes[node.uuid]; exists {
			if !reflect.DeepEqual(previous.record, record) {
				return transcript.Document{}, fmt.Errorf("conflicting duplicate Claude uuid %q at %s:%d", node.uuid, path, line)
			}
			previous.index = index
			continue
		}
		node.index = index
		nodes[node.uuid] = node
		ordered = append(ordered, node)
	}
	if len(nodes) == 0 {
		return transcript.Document{}, fmt.Errorf("Claude session contains no conversation graph records")
	}

	// Validate ancestry and propagate agent privacy through nonmessage records.
	// Meta/context records are hidden but can be ancestors of genuine public
	// messages (for example the local-command caveat before a /compact turn).
	// A nominally primary message beneath another agent must never become a tip.
	state := make(map[string]int)
	var primary func(*claudeNode) (bool, error)
	primary = func(node *claudeNode) (bool, error) {
		if state[node.uuid] == 1 {
			return false, fmt.Errorf("Claude transcript has cyclic parentUuid ancestry near %q", node.uuid)
		}
		if state[node.uuid] == 2 {
			return !node.agentPrivate, nil
		}
		state[node.uuid] = 1
		if node.hasParent {
			parent, ok := nodes[node.parent]
			if !ok {
				return false, fmt.Errorf("Claude transcript has missing ancestry: node %q references missing parent %q", node.uuid, node.parent)
			}
			allowed, err := primary(parent)
			if err != nil {
				return false, err
			}
			if !allowed {
				node.private = true
				node.agentPrivate = true
			}
		}
		state[node.uuid] = 2
		return !node.agentPrivate, nil
	}
	for _, node := range ordered {
		if _, err := primary(node); err != nil {
			return transcript.Document{}, err
		}
	}
	// A leaf is a primary user/assistant without a primary message descendant.
	// Terminal attachments/system records do not hide its last message; private
	// descendants do not supersede it. Tool-only and local-command messages do
	// participate in branch selection, although they render no public text.
	hasMessageDescendant := make(map[string]bool)
	for _, node := range ordered {
		if node.private || node.role == "" {
			continue
		}
		for current := node; current.hasParent; {
			current = nodes[current.parent]
			if hasMessageDescendant[current.uuid] {
				break
			}
			hasMessageDescendant[current.uuid] = true
		}
	}
	var selected *claudeNode
	for _, node := range ordered {
		if node.private || node.role == "" || hasMessageDescendant[node.uuid] {
			continue
		}
		if selected == nil || node.index > selected.index {
			selected = node
		}
	}
	if selected == nil {
		return transcript.Document{}, fmt.Errorf("no public user or assistant text in this Claude session")
	}
	branch, err := claudeBranch(nodes, selected.uuid)
	if err != nil {
		return transcript.Document{}, err
	}
	var messages []string
	for index := len(branch) - 1; index >= 0; index-- {
		node := branch[index]
		if node.private || node.text == "" {
			continue
		}
		label := "User"
		if node.role == "assistant" {
			label = "Claude"
		}
		if node.compact {
			label = "Context compacted"
		}
		messages = append(messages, fmt.Sprintf("## %s\n\n%s", label, node.text))
	}
	return transcript.NewDocument("Claude", sessionID, messages, partial)
}

type claudeNode struct {
	uuid         string
	parent       string
	hasParent    bool
	role         string
	text         string
	private      bool
	agentPrivate bool
	compact      bool
	index        int
	record       map[string]any
}

func claudeKnownRecordType(typ string) bool {
	switch typ {
	case "user", "assistant", "system", "progress", "attachment", "last-prompt", "mode", "permission-mode", "ai-title", "file-history-snapshot", "summary", "custom-title", "queue-operation", "agent-name", "atis-latch":
		return true
	default:
		return false
	}
}

func claudeGraphRecordType(typ string) bool {
	switch typ {
	case "user", "assistant", "system", "progress", "attachment":
		return true
	default:
		return false
	}
}

func claudeHasTranscriptShape(record map[string]any) bool {
	for _, key := range []string{"uuid", "parentUuid", "logicalParentUuid", "message", "isSidechain", "isMeta", "agentId", "teamName", "isCompactSummary", "isVisibleInTranscriptOnly"} {
		if recordHasKey(record, key) {
			return true
		}
	}
	return false
}

func recordHasKey(record map[string]any, key string) bool { _, present := record[key]; return present }

// Outer sessionId is the canonical storage identity. Wire session_id may
// legitimately retain the original ID after /clear, a copied fork or attachment.
func claudeSessionID(path string, records []map[string]any) (string, error) {
	ids := make(map[string]struct{})
	for index, record := range records {
		value, present := record["sessionId"]
		if !present {
			continue
		}
		sid, ok := value.(string)
		if !ok || !agents.ValidID(sid) {
			return "", fmt.Errorf("invalid Claude sessionId at %s:%d", path, index+1)
		}
		ids[sid] = struct{}{}
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("Claude session is missing sessionId")
	}
	if len(ids) != 1 {
		var values []string
		for id := range ids {
			values = append(values, id)
		}
		sort.Strings(values)
		return "", fmt.Errorf("Claude transcript contains multiple sessionId values: %s", strings.Join(values, ", "))
	}
	for id := range ids {
		return id, nil
	}
	return "", fmt.Errorf("Claude session is missing sessionId")
}

func claudeNodeFor(path string, line int, record map[string]any, typ, sessionID string) (*claudeNode, error) {
	if transcript.String(record, "sessionId") != sessionID {
		return nil, fmt.Errorf("Claude %s record is missing or mismatches sessionId at %s:%d", typ, path, line)
	}
	if alias, present := record["session_id"]; present {
		id, ok := alias.(string)
		if !ok || !agents.ValidID(id) {
			return nil, fmt.Errorf("invalid Claude session_id at %s:%d", path, line)
		}
	}
	uuid, ok := record["uuid"].(string)
	if !ok || !agents.ValidID(uuid) {
		return nil, fmt.Errorf("Claude %s record is missing or has invalid uuid at %s:%d", typ, path, line)
	}
	parent, hasParent, err := claudeParent(record)
	if err != nil {
		return nil, fmt.Errorf("%w at %s:%d", err, path, line)
	}
	node := &claudeNode{uuid: uuid, parent: parent, hasParent: hasParent, record: record}
	for _, key := range []string{"isSidechain", "isMeta", "isCompactSummary", "isVisibleInTranscriptOnly"} {
		flag, err := claudeOptionalBool(record, key, path, line)
		if err != nil {
			return nil, err
		}
		if (key == "isSidechain" || key == "isMeta") && flag {
			node.private = true
		}
		if key == "isSidechain" && flag {
			node.agentPrivate = true
		}
		if key == "isCompactSummary" && flag {
			node.compact = true
		}
		if key == "isVisibleInTranscriptOnly" && flag && !transcript.Bool(record, "isCompactSummary") {
			node.private = true
		}
	}
	for _, key := range []string{"userType", "agentId", "teamName"} {
		if value, present := record[key]; present {
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("invalid Claude %s at %s:%d", key, path, line)
			}
			if text != "" && (key != "userType" || text != "external") {
				node.private = true
				if key != "userType" {
					node.agentPrivate = true
				}
			}
		}
	}
	if typ == "system" && transcript.String(record, "subtype") == "compact_boundary" {
		// Explicit reset only. logicalParentUuid describes the old history but
		// is not an edge in the compacted conversation.
		if node.hasParent {
			return nil, fmt.Errorf("Claude compact_boundary must reset parentUuid at %s:%d", path, line)
		}
	}
	if typ != "user" && typ != "assistant" {
		return node, nil
	}
	message, ok := record["message"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid Claude %s message at %s:%d: expected object", typ, path, line)
	}
	role, ok := message["role"].(string)
	if !ok || role != typ {
		return nil, fmt.Errorf("invalid Claude %s message role at %s:%d", typ, path, line)
	}
	content, present := message["content"]
	if !present {
		return nil, fmt.Errorf("invalid Claude %s message content at %s:%d: missing content", typ, path, line)
	}
	text, err := claudeMessageText(typ, content, path, line)
	if err != nil {
		return nil, err
	}
	node.role, node.text = role, text
	return node, nil
}

func claudeParent(record map[string]any) (string, bool, error) {
	value, present := record["parentUuid"]
	if !present || value == nil {
		return "", false, nil
	}
	parent, ok := value.(string)
	if !ok || !agents.ValidID(parent) {
		return "", false, fmt.Errorf("invalid Claude parentUuid")
	}
	return parent, true, nil
}

func claudeOptionalBool(record map[string]any, key, path string, line int) (bool, error) {
	value, present := record[key]
	if !present {
		return false, nil
	}
	result, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("invalid Claude %s at %s:%d", key, path, line)
	}
	return result, nil
}

func claudeLocalCommand(text string) bool {
	text = strings.TrimSpace(text)
	for _, prefix := range []string{"<command-name>", "<local-command-stdout>", "<local-command-caveat>"} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func claudeMessageText(role string, content any, path string, line int) (string, error) {
	if role == "user" {
		if text, ok := content.(string); ok {
			if claudeLocalCommand(text) {
				return "", nil
			}
			return strings.TrimSpace(text), nil
		}
	}
	blocks, ok := content.([]any)
	if !ok {
		return "", fmt.Errorf("unsupported Claude %s message content at %s:%d: expected string or block list", role, path, line)
	}
	parts := make([]string, 0, len(blocks))
	for index, raw := range blocks {
		block, ok := raw.(map[string]any)
		if !ok {
			return "", fmt.Errorf("invalid Claude %s content block %d at %s:%d", role, index, path, line)
		}
		typ, ok := block["type"].(string)
		if !ok || typ == "" {
			return "", fmt.Errorf("invalid Claude %s content block %d at %s:%d: missing type", role, index, path, line)
		}
		switch typ {
		case "text":
			text, ok := block["text"].(string)
			if !ok {
				return "", fmt.Errorf("invalid Claude %s text block %d at %s:%d", role, index, path, line)
			}
			if !claudeLocalCommand(text) {
				parts = append(parts, text)
			}
		case "image", "document", "thinking", "redacted_thinking", "tool_use", "tool_result":
			// Only public text is rendered, never reasoning, media or tools.
		default:
			return "", fmt.Errorf("unsupported Claude %s content block type %q at %s:%d", role, typ, path, line)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n")), nil
}

func claudeBranch(nodes map[string]*claudeNode, leafID string) ([]*claudeNode, error) {
	var branch []*claudeNode
	seen := make(map[string]bool)
	current := leafID
	for {
		if seen[current] {
			return nil, fmt.Errorf("Claude transcript has cyclic parentUuid ancestry near %q", current)
		}
		seen[current] = true
		node, ok := nodes[current]
		if !ok {
			return nil, fmt.Errorf("Claude transcript has missing ancestry: node %q references missing parent %q", leafID, current)
		}
		branch = append(branch, node)
		if !node.hasParent {
			break
		}
		current = node.parent
	}
	return branch, nil
}
