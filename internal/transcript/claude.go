package transcript

import (
	"fmt"
	"sort"
	"strings"
)

// RenderClaude renders the public user and assistant messages in a Claude
// JSONL session. Claude stores each record in a parentUuid graph rather than a
// flat message list; only a unique persisted public tip is rendered.
func RenderClaude(path string) (Document, error) {
	records, partial, err := ReadJSONL(path)
	if err != nil {
		return Document{}, err
	}
	if len(records) == 0 {
		return Document{}, fmt.Errorf("empty Claude session file: %s", path)
	}

	sessionID, err := claudeSessionID(path, records)
	if err != nil {
		return Document{}, err
	}

	nodes := make(map[string]*claudeNode)
	ordered := make([]*claudeNode, 0, len(records))
	for index, record := range records {
		line := index + 1
		typ, ok := record["type"].(string)
		if !ok || typ == "" {
			return Document{}, fmt.Errorf("invalid Claude record type at %s:%d", path, line)
		}
		if !claudeKnownRecordType(typ) {
			// Claude adds metadata record types over time. Metadata with no
			// graph/message fields is safe to ignore, while an unknown record
			// that could affect transcript selection must fail closed.
			if claudeHasTranscriptShape(record) {
				return Document{}, fmt.Errorf("unsupported Claude record type %q at %s:%d", typ, path, line)
			}
			continue
		}

		// Metadata records do not participate in the conversation graph. A
		// progress/system/attachment record only participates when Claude
		// persisted a UUID for it; metadata-only records may omit one.
		if !claudeGraphRecordType(typ) {
			continue
		}
		if (typ != "user" && typ != "assistant") && !recordHasKey(record, "uuid") {
			continue
		}

		node, err := claudeNodeFor(path, line, record, typ, sessionID)
		if err != nil {
			return Document{}, err
		}
		if _, exists := nodes[node.uuid]; exists {
			return Document{}, fmt.Errorf("duplicate Claude uuid %q at %s:%d", node.uuid, path, line)
		}
		nodes[node.uuid] = node
		ordered = append(ordered, node)
	}
	if len(nodes) == 0 {
		return Document{}, fmt.Errorf("Claude session contains no conversation graph records")
	}

	children := make(map[string][]string, len(nodes))
	for _, node := range ordered {
		if node.hasParent {
			if _, exists := nodes[node.parent]; exists {
				children[node.parent] = append(children[node.parent], node.uuid)
			}
		}
	}

	public := make(map[string]bool, len(nodes))
	for _, node := range ordered {
		public[node.uuid] = node.public
	}
	candidates, err := claudePublicTips(ordered, children, public)
	if err != nil {
		return Document{}, err
	}

	switch len(candidates) {
	case 0:
		return Document{}, fmt.Errorf("no public user or assistant text in this Claude session")
	case 1:
		// Continue with the only persisted public tip.
	default:
		return Document{}, fmt.Errorf("Claude session has multiple public branch tips; candidate tips: %s", strings.Join(candidates, ", "))
	}
	selected := candidates[0]
	branch, err := claudeBranch(nodes, selected)
	if err != nil {
		return Document{}, err
	}
	messages := make([]string, 0, len(branch))
	for index := len(branch) - 1; index >= 0; index-- {
		node := branch[index]
		if !node.public {
			continue
		}
		label := "User"
		if node.role == "assistant" {
			label = "Claude"
		}
		messages = append(messages, fmt.Sprintf("## %s\n\n%s", label, node.text))
	}
	if len(messages) == 0 {
		return Document{}, fmt.Errorf("no public user or assistant text in this Claude branch")
	}
	return NewDocument("Claude", sessionID, messages, partial)
}

type claudeNode struct {
	uuid       string
	parent     string
	hasParent  bool
	role       string
	text       string
	public     bool
	recordType string
}

func claudeKnownRecordType(typ string) bool {
	switch typ {
	case "user", "assistant", "system", "progress", "attachment", "last-prompt", "mode", "permission-mode", "ai-title", "file-history-snapshot", "summary", "custom-title", "queue-operation":
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
	for _, key := range []string{"uuid", "parentUuid", "message", "isSidechain", "isMeta"} {
		if recordHasKey(record, key) {
			return true
		}
	}
	return false
}

func recordHasKey(record map[string]any, key string) bool {
	_, present := record[key]
	return present
}

func claudeSessionID(path string, records []map[string]any) (string, error) {
	ids := make(map[string]struct{})
	for index, record := range records {
		value, present := record["sessionId"]
		if !present {
			continue
		}
		sid, ok := value.(string)
		if !ok || sid == "" {
			return "", fmt.Errorf("invalid Claude sessionId at %s:%d", path, index+1)
		}
		ids[sid] = struct{}{}
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("Claude session is missing sessionId")
	}
	if len(ids) != 1 {
		values := make([]string, 0, len(ids))
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

func claudeNodeFor(path string, line int, record map[string]any, typ string, sessionID string) (*claudeNode, error) {
	sid, ok := record["sessionId"].(string)
	if !ok || sid == "" {
		return nil, fmt.Errorf("Claude %s record is missing sessionId at %s:%d", typ, path, line)
	}
	if sid != sessionID {
		return nil, fmt.Errorf("Claude %s record sessionId %q does not match session %q at %s:%d", typ, sid, sessionID, path, line)
	}
	if alias, present := record["session_id"]; present {
		aliasID, ok := alias.(string)
		if !ok || aliasID == "" {
			return nil, fmt.Errorf("invalid Claude session_id at %s:%d", path, line)
		}
		if aliasID != sessionID {
			return nil, fmt.Errorf("Claude %s record session_id %q does not match session %q at %s:%d", typ, aliasID, sessionID, path, line)
		}
	}

	uuid, ok := record["uuid"].(string)
	if !ok || uuid == "" {
		return nil, fmt.Errorf("Claude %s record is missing uuid at %s:%d", typ, path, line)
	}
	parent, hasParent, err := claudeParent(record)
	if err != nil {
		return nil, fmt.Errorf("%w at %s:%d", err, path, line)
	}
	sidechain, err := claudeOptionalBool(record, "isSidechain", path, line)
	if err != nil {
		return nil, err
	}
	meta, err := claudeOptionalBool(record, "isMeta", path, line)
	if err != nil {
		return nil, err
	}
	if userType, present := record["userType"]; present {
		value, ok := userType.(string)
		if !ok {
			return nil, fmt.Errorf("invalid Claude userType at %s:%d", path, line)
		}
		// Claude marks persisted user-facing records as external. Internal
		// records are injected context and must not become public transcript.
		if value != "" && value != "external" {
			meta = true
		}
	}

	node := &claudeNode{
		uuid:       uuid,
		parent:     parent,
		hasParent:  hasParent,
		recordType: typ,
	}
	if sidechain || meta || (typ != "user" && typ != "assistant") {
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
	node.role = role
	node.text = text
	node.public = text != ""
	return node, nil
}

func claudeParent(record map[string]any) (string, bool, error) {
	value, present := record["parentUuid"]
	if !present || value == nil {
		return "", false, nil
	}
	parent, ok := value.(string)
	if !ok || parent == "" {
		return "", false, fmt.Errorf("invalid Claude parentUuid")
	}
	return parent, true, nil
}

func claudeOptionalBool(record map[string]any, key string, path string, line int) (bool, error) {
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

func claudeMessageText(role string, content any, path string, line int) (string, error) {
	if role == "user" {
		if text, ok := content.(string); ok {
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
			parts = append(parts, text)
		case "image":
			parts = append(parts, "[Image attachment omitted]")
		case "thinking", "tool_use", "tool_result":
			// Reasoning, tool calls, and tool results are intentionally private.
		default:
			return "", fmt.Errorf("unsupported Claude %s content block type %q at %s:%d", role, typ, path, line)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n")), nil
}

func claudePublicTips(ordered []*claudeNode, children map[string][]string, public map[string]bool) ([]string, error) {
	memo := make(map[string]bool, len(ordered))
	visiting := make(map[string]bool, len(ordered))
	var hasPublicDescendant func(string) (bool, error)
	hasPublicDescendant = func(id string) (bool, error) {
		if value, ok := memo[id]; ok {
			return value, nil
		}
		if visiting[id] {
			return false, fmt.Errorf("Claude transcript has cyclic parentUuid graph near %q", id)
		}
		visiting[id] = true
		for _, child := range children[id] {
			if public[child] {
				memo[id] = true
				delete(visiting, id)
				return true, nil
			}
			found, err := hasPublicDescendant(child)
			if err != nil {
				return false, err
			}
			if found {
				memo[id] = true
				delete(visiting, id)
				return true, nil
			}
		}
		memo[id] = false
		delete(visiting, id)
		return false, nil
	}

	candidates := make([]string, 0)
	for _, node := range ordered {
		if !public[node.uuid] {
			continue
		}
		found, err := hasPublicDescendant(node.uuid)
		if err != nil {
			return nil, err
		}
		if !found {
			candidates = append(candidates, node.uuid)
		}
	}
	return candidates, nil
}

func claudeBranch(nodes map[string]*claudeNode, leafID string) ([]*claudeNode, error) {
	branch := make([]*claudeNode, 0)
	seen := make(map[string]bool)
	current := leafID
	for {
		if seen[current] {
			return nil, fmt.Errorf("Claude transcript has cyclic parentUuid ancestry near %q", current)
		}
		seen[current] = true
		node, ok := nodes[current]
		if !ok {
			return nil, fmt.Errorf("Claude transcript has missing ancestry: node %q references missing parent %q; compacted session ancestry is unsupported", leafID, current)
		}
		branch = append(branch, node)
		if !node.hasParent {
			break
		}
		if _, ok := nodes[node.parent]; !ok {
			return nil, fmt.Errorf("Claude transcript has missing ancestry: node %q references missing parent %q; compacted session ancestry is unsupported", node.uuid, node.parent)
		}
		current = node.parent
	}
	return branch, nil
}
