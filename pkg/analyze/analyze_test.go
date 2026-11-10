package analyze

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/geekychris/claude-session-analyzer/pkg/parse"
	"github.com/geekychris/claude-session-analyzer/pkg/pricing"
)

func load(t *testing.T) SessionAnalysis {
	t.Helper()
	f, err := os.Open("testdata/basic.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	recs, err := parse.Records(f)
	if err != nil {
		t.Fatal(err)
	}
	return Session(recs, Options{})
}

func TestSessionBasics(t *testing.T) {
	a := load(t)
	if a.SessionID != "11111111-2222-3333-4444-555555555555" || a.CWD != "/work/demo" || a.GitBranch != "feat/x" {
		t.Errorf("identity %q %q %q", a.SessionID, a.CWD, a.GitBranch)
	}
	if a.Title != "Greeting function" || len(a.PRLinks) != 1 {
		t.Errorf("title %q pr %v", a.Title, a.PRLinks)
	}
	if a.Records != 26 || len(a.Messages) != 26 || a.UserTurns != 3 {
		t.Errorf("records %d messages %d turns %d", a.Records, len(a.Messages), a.UserTurns)
	}
	if len(a.ToolCalls) != 8 {
		t.Errorf("tool calls %d", len(a.ToolCalls))
	}
	want := map[string]int{"read": 1, "search": 2, "modify": 2, "bash": 2, "subagent": 1}
	for k, v := range want {
		if a.ToolCounts[k] != v {
			t.Errorf("tool count %s = %d want %d", k, a.ToolCounts[k], v)
		}
	}
	for _, m := range a.Messages {
		if strings.Contains(m.Text, "secret thoughts") {
			t.Fatal("thinking leaked into text")
		}
	}
}

func TestTokensDedupedByResponse(t *testing.T) {
	a := load(t)
	// msg_1 appears on two records with the same usage; counted once.
	if a.Tokens.Input != 190 || a.Tokens.Output != 295 || a.Tokens.CacheRead != 1000 || a.Tokens.CacheCreation != 200 {
		t.Fatalf("tokens %+v", a.Tokens)
	}
	if a.AssistantMessages != 9 {
		t.Errorf("assistant responses %d", a.AssistantMessages)
	}
	if a.PrimaryModel != "claude-sonnet-4-5" || len(a.Models) != 3 {
		t.Fatalf("models %+v", a.Models)
	}
	var cost float64
	cost += pricing.CostUSD("claude-sonnet-4-5", 130, 165, 1000, 200)
	cost += pricing.CostUSD("claude-opus-4-1", 30, 125, 0, 0)
	cost += pricing.CostUSD("claude-haiku-4-5", 30, 5, 0, 0)
	if math.Abs(a.CostUSD-cost) > 1e-9 {
		t.Fatalf("cost %v want %v", a.CostUSD, cost)
	}
}

func TestTimings(t *testing.T) {
	a := load(t)
	if a.FirstTimestamp != "2026-11-01T10:00:00Z" || a.LastTimestamp != "2026-11-01T10:22:00Z" {
		t.Errorf("span %s %s", a.FirstTimestamp, a.LastTimestamp)
	}
	if a.WallSeconds != 1320 {
		t.Errorf("wall %v", a.WallSeconds)
	}
	// 66 s before the 20-minute gap, 54 s after; the gap itself is idle.
	if a.ActiveSeconds != 120 {
		t.Errorf("active %v", a.ActiveSeconds)
	}
}

func TestEpisodes(t *testing.T) {
	a := load(t)
	if len(a.Episodes) != 3 {
		t.Fatalf("episodes %d", len(a.Episodes))
	}
	e := a.Episodes[0]
	if !e.Ended || e.ToolsBeforeEdit != 2 || e.EditFilePath != "/work/demo/hello.go" {
		t.Errorf("ep1 %+v", e.Episode)
	}
	if strings.Join(e.FilesEdited, ",") != "/work/demo/hello.go,/work/demo/hello_test.go" {
		t.Errorf("edited %v", e.FilesEdited)
	}
	if e.Errors != 1 || e.Retries != 1 || e.UserPrompt != "Add a greeting function to hello.go and test it" {
		t.Errorf("ep1 errors %d retries %d prompt %q", e.Errors, e.Retries, e.UserPrompt)
	}
	if e.FinalText != "Added Greet() and a test; tests pass." || len(e.Statements) != 2 {
		t.Errorf("ep1 statements %v final %q", e.Statements, e.FinalText)
	}
	if e.Tokens.Output != 165 {
		t.Errorf("ep1 tokens %+v", e.Tokens)
	}
	e = a.Episodes[1]
	if e.Ended || e.ToolCounts["subagent"] != 1 || e.ToolCounts["search"] != 1 {
		t.Errorf("ep2 %+v", e)
	}
	if a.Subagents.Calls != 1 || a.Subagents.SidechainMessages != 3 || a.Subagents.Tasks[0] != "Survey test layout" {
		t.Errorf("subagents %+v", a.Subagents)
	}
	if a.Retries != 1 || len(a.Errors) != 2 {
		t.Errorf("retries %d errors %+v", a.Retries, a.Errors)
	}
	if !a.Errors[0].IsRetried || a.Errors[0].Kind != "tool_error" || a.Errors[1].Kind != "api_error" {
		t.Errorf("errors %+v", a.Errors)
	}
	if a.FinalText != "Tests live next to the code in hello_test.go." {
		t.Errorf("final %q", a.FinalText)
	}
	if a.Files[0].Path != "/work/demo/hello.go" || a.Files[0].Reads != 1 || a.Files[0].Edits != 1 {
		t.Errorf("files %+v", a.Files)
	}
	if a.EpisodeBuckets[0].Count+a.EpisodeBuckets[1].Count != 3 {
		t.Errorf("buckets %+v", a.EpisodeBuckets)
	}
	// message → episode attribution
	for _, m := range a.Messages {
		if m.UUID == "s2" && m.Episode != 2 {
			t.Errorf("sidechain message episode %d", m.Episode)
		}
	}
}

func TestEmptyAndJSON(t *testing.T) {
	a := Session(nil, Options{})
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"episodes":[]`) || !strings.Contains(string(b), `"models":[]`) {
		t.Fatalf("empty analysis should marshal to empty lists: %s", b)
	}
}

func TestMaxTextLen(t *testing.T) {
	recs := []parse.Record{{Type: "user", Message: &parse.MessagePayload{Role: "user", Content: json.RawMessage(`"` + strings.Repeat("x", 100) + `"`)}}}
	a := Session(recs, Options{MaxTextLen: 10})
	if !a.Messages[0].Truncated || len(a.Messages[0].Text) > 14 {
		t.Fatalf("%+v", a.Messages[0])
	}
}
