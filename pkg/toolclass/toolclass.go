// Package toolclass classifies Claude Code tool calls: modifying (Edit,
// Write, ...), read, search, bash, sub-agent; extracts the primary file path;
// and for Bash, the program and action that ran.
package toolclass

import (
	"encoding/json"
	"strings"
)

// ToolFacts is the classification of one tool_use, with the primary
// file_path where applicable.
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
	"Bash":       true,
	"BashOutput": true,
}

// subagentTools invoke another agent — increases context "depth."
var subagentTools = map[string]bool{
	"Agent":      true,
	"Task":       true,
	"TaskCreate": true,
	"TaskUpdate": true,
	"TaskGet":    true,
	"TaskList":   true,
	"TaskOutput": true,
	"TaskStop":   true,
	"Workflow":   true,
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

// Categories, in the order they are reported. A tool call has exactly one.
const (
	CategoryModify   = "modify"
	CategoryRead     = "read"
	CategorySearch   = "search"
	CategoryBash     = "bash"
	CategorySubagent = "subagent"
	CategoryOther    = "other"
)

// Categories lists every category in display order.
var Categories = []string{CategoryRead, CategorySearch, CategoryModify, CategoryBash, CategorySubagent, CategoryOther}

// Category collapses the flags to one label. Modifying wins over everything
// (it bounds an episode); then sub-agent, bash, search, read.
func (tf ToolFacts) Category() string {
	switch {
	case tf.IsModifying:
		return CategoryModify
	case tf.IsSubagent:
		return CategorySubagent
	case tf.IsBash:
		return CategoryBash
	case tf.IsSearch:
		return CategorySearch
	case tf.IsRead:
		return CategoryRead
	}
	return CategoryOther
}
