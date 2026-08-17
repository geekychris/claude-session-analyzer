// Package parser decodes Claude Code JSONL session files.
//
// Events in a JSONL file are heterogeneous — user messages, assistant messages
// (with tool_use / thinking / text content), system events (turn_duration,
// compact_boundary, api_error, ...), plus header records like `mode`,
// `permission-mode`, `file-history-snapshot`, `ai-title`, `pr-link`.
//
// We keep decoding permissive: only strongly-typed fields we care about are
// pulled out; the full raw JSON is preserved for downstream consumers.
package parser

import "encoding/json"

// Event is the union of all event shapes that appear in a session JSONL file.
// Fields not relevant to a given event type are left zero.
type Event struct {
	// Line offset in the source file (bytes) where this event started.
	// Populated by the parser, not present in the JSON itself.
	Offset int64 `json:"-"`
	// Raw JSON of the line, for storage/debugging.
	Raw json.RawMessage `json:"-"`

	Type      string `json:"type"`
	SessionID string `json:"sessionId"`

	// Common fields for user/assistant/system messages
	UUID        string `json:"uuid,omitempty"`
	ParentUUID  string `json:"parentUuid,omitempty"`
	Timestamp   string `json:"timestamp,omitempty"`
	IsSidechain bool   `json:"isSidechain,omitempty"`
	IsMeta      bool   `json:"isMeta,omitempty"`
	CWD         string `json:"cwd,omitempty"`
	GitBranch   string `json:"gitBranch,omitempty"`
	Version     string `json:"version,omitempty"`

	// User/assistant messages carry a nested `message` object.
	Message *MessagePayload `json:"message,omitempty"`

	// system subtype (turn_duration, compact_boundary, ...)
	Subtype  string `json:"subtype,omitempty"`
	Content  json.RawMessage `json:"content,omitempty"`  // system.content can be string OR list
	Duration int64  `json:"durationMs,omitempty"`

	// Headers
	Mode           string `json:"mode,omitempty"`
	PermissionMode string `json:"permissionMode,omitempty"`
	AITitle        string `json:"aiTitle,omitempty"`

	// pr-link
	PRNumber     int    `json:"prNumber,omitempty"`
	PRUrl        string `json:"prUrl,omitempty"`
	PRRepository string `json:"prRepository,omitempty"`
}

// MessagePayload is the `message` object nested inside user/assistant events.
// - For user events with role=user, `content` is either a string OR an array
//   of ContentBlock (when it contains tool_result blocks).
// - For assistant events, `content` is always an array of ContentBlock.
type MessagePayload struct {
	Role    string          `json:"role"`
	Model   string          `json:"model,omitempty"`
	ID      string          `json:"id,omitempty"`
	Type    string          `json:"type,omitempty"`
	Content json.RawMessage `json:"content"`
	Usage   *Usage          `json:"usage,omitempty"`
}

// Usage is the token accounting block on assistant messages.
type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

// ContentBlock is one entry in a message.content array.
// Only fields relevant per-type are populated; the rest stay zero.
type ContentBlock struct {
	Type string `json:"type"`

	// text
	Text string `json:"text,omitempty"`

	// thinking
	Thinking string `json:"thinking,omitempty"`

	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result
	ToolUseID       string          `json:"tool_use_id,omitempty"`
	IsError         bool            `json:"is_error,omitempty"`
	ResultContent   json.RawMessage `json:"content,omitempty"` // string or list; use `Content` collision-free
}

// DecodeContentBlocks parses a message content field into blocks.
// Returns nil if content is a plain string (the caller can handle it as text).
func DecodeContentBlocks(raw json.RawMessage) ([]ContentBlock, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	// Peek at first non-whitespace byte to distinguish array vs string.
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '[':
			var out []ContentBlock
			if err := json.Unmarshal(raw, &out); err != nil {
				return nil, false, err
			}
			return out, true, nil
		case '"':
			// plain string content
			return nil, false, nil
		default:
			return nil, false, nil
		}
	}
	return nil, false, nil
}

// StringContent decodes raw content as a plain string, best-effort.
// Returns "" if the content isn't a string.
func StringContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return ""
}
