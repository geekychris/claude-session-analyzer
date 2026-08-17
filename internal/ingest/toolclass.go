package ingest

import (
	"encoding/json"
	"strings"
)

// Classifier turns a raw tool_use into the flags we track and pulls out the
// primary file_path where applicable.
type ToolFacts struct {
	Name        string
	FilePath    string
	IsModifying bool
	IsRead      bool
	IsBash      bool
	IsSearch    bool
	IsSubagent  bool
}

// modifyingTools write to disk. Anything here bounds an edit episode.
var modifyingTools = map[string]bool{
	"Edit":         true,
	"Write":        true,
	"MultiEdit":    true,
	"NotebookEdit": true,
}

// readTools open files/notebooks without modifying them.
var readTools = map[string]bool{
	"Read":         true,
	"NotebookRead": true,
}

// searchTools scan without opening a specific file.
var searchTools = map[string]bool{
	"Grep": true,
	"Glob": true,
}

// bashTools shell out.
var bashTools = map[string]bool{
	"Bash":    true,
	"BashOutput": true,
}

// subagentTools invoke another agent — increases context "depth."
var subagentTools = map[string]bool{
	"Agent":       true,
	"Task":        true,
	"TaskCreate":  true,
	"TaskUpdate":  true,
	"TaskGet":     true,
	"TaskList":    true,
	"TaskOutput":  true,
	"TaskStop":    true,
	"Workflow":    true,
}

// ClassifyTool applies the tool taxonomy and extracts a file_path if the input
// carries one. `input` is the raw JSON of tool_use.input.
func ClassifyTool(name string, input json.RawMessage) ToolFacts {
	tf := ToolFacts{Name: name}
	tf.IsModifying = modifyingTools[name]
	tf.IsRead = readTools[name]
	tf.IsSearch = searchTools[name]
	tf.IsBash = bashTools[name]
	tf.IsSubagent = subagentTools[name] || strings.HasPrefix(name, "mcp__")

	if len(input) == 0 {
		return tf
	}
	// Peek at a few common keys — cheap decode.
	var m map[string]json.RawMessage
	if err := json.Unmarshal(input, &m); err != nil {
		return tf
	}
	for _, key := range []string{"file_path", "notebook_path", "path"} {
		if v, ok := m[key]; ok {
			var s string
			if err := json.Unmarshal(v, &s); err == nil && s != "" {
				tf.FilePath = s
				return tf
			}
		}
	}
	// Glob has "pattern"; Grep has "pattern" + optional "path". We record path if present.
	if v, ok := m["pattern"]; ok && !tf.IsRead && !tf.IsModifying {
		var s string
		if err := json.Unmarshal(v, &s); err == nil {
			tf.FilePath = s
		}
	}
	return tf
}
