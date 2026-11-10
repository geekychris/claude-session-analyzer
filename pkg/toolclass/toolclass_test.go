package toolclass

import (
	"encoding/json"
	"testing"
)

func TestClassifyAndCategory(t *testing.T) {
	cases := []struct {
		name, input, cat, path string
	}{
		{"Edit", `{"file_path":"/a.go"}`, CategoryModify, "/a.go"},
		{"Write", `{"file_path":"/b.go"}`, CategoryModify, "/b.go"},
		{"NotebookEdit", `{"notebook_path":"/n.ipynb"}`, CategoryModify, "/n.ipynb"},
		{"Read", `{"file_path":"/c.go"}`, CategoryRead, "/c.go"},
		{"Grep", `{"pattern":"foo","path":"/src"}`, CategorySearch, "/src"},
		{"Glob", `{"pattern":"**/*.go"}`, CategorySearch, "**/*.go"},
		{"Bash", `{"command":"ls"}`, CategoryBash, ""},
		{"Task", `{"description":"x"}`, CategorySubagent, ""},
		{"mcp__github__get_pr", `{}`, CategorySubagent, ""},
		{"WebFetch", `{"url":"https://x"}`, CategoryOther, ""},
	}
	for _, c := range cases {
		tf := ClassifyTool(c.name, json.RawMessage(c.input))
		if tf.Category() != c.cat || tf.FilePath != c.path {
			t.Errorf("%s: category %s path %q, want %s %q", c.name, tf.Category(), tf.FilePath, c.cat, c.path)
		}
	}
}

func TestClassifyBash(t *testing.T) {
	cases := map[string][2]string{
		"git commit -m x":                {"git", "commit"},
		"FOO=1 sudo docker run alpine":   {"docker", "run"},
		"# comment\ncd /x && go test ./": {"cd", ""},
		"for f in *; do echo $f; done":   {"f", ""},
		"/usr/local/bin/npm install":     {"npm", "install"},
	}
	for cmd, want := range cases {
		p, a := ClassifyBashCommand(cmd)
		if p != want[0] || a != want[1] {
			t.Errorf("%q: got %s/%s want %s/%s", cmd, p, a, want[0], want[1])
		}
	}
	p, a := ClassifyBashInput(json.RawMessage(`{"command":"gh pr create"}`))
	if p != "gh" || a != "pr" {
		t.Errorf("got %s %s", p, a)
	}
}
