package episodes

import "testing"

func TestDerive(t *testing.T) {
	msgs := []Message{
		{ID: 1, Role: "meta"},
		{ID: 2, Role: "user", Ts: "2026-01-01T00:00:00Z", Text: "do it"},
		{ID: 3, Role: "assistant", Ts: "2026-01-01T00:00:01Z", InputTokens: 10, OutputTokens: 5},
		{ID: 4, Role: "user", Subtype: "tool_result", Ts: "2026-01-01T00:00:02Z"},
		{ID: 5, Role: "assistant", Ts: "2026-01-01T00:00:04Z", OutputTokens: 7},
		{ID: 6, Role: "user", Ts: "2026-01-01T00:01:00Z", Text: "more"},
		{ID: 7, Role: "assistant", Ts: "2026-01-01T00:01:03Z"},
	}
	tcs := []ToolCall{
		{ID: 10, MessageID: 3, Name: "Read", FilePath: "/a", IsRead: true, ResultSize: 100},
		{ID: 11, MessageID: 3, Name: "Grep", FilePath: "x", IsSearch: true, ResultSize: 50},
		{ID: 12, MessageID: 5, Name: "Edit", FilePath: "/a", IsModifying: true, Ts: "2026-01-01T00:00:04Z"},
		{ID: 13, MessageID: 5, Name: "Write", FilePath: "/tmp/helper.py", IsModifying: true},
		{ID: 14, MessageID: 5, Name: "Bash", IsBash: true},
		{ID: 15, MessageID: 7, Name: "Task", IsSubagent: true, Ts: "2026-01-01T00:01:03Z"},
	}
	eps := Derive(msgs, tcs, "/work")
	if len(eps) != 2 {
		t.Fatalf("episodes = %d", len(eps))
	}
	e := eps[0]
	if e.Seq != 1 || !e.Ended || e.ToolCallCount != 2 || e.FirstEditToolCallID != 12 || e.EditFilePath != "/a" {
		t.Fatalf("ep1 %+v", e)
	}
	if e.BytesReadBeforeEdit != 150 || e.BashCount != 1 || e.SearchCount != 1 || len(e.ToolCallIDs) != 5 {
		t.Fatalf("ep1 counts %+v", e)
	}
	if e.DurationMs != 4000 || e.InputTokens != 10 || e.OutputTokens != 12 {
		t.Fatalf("ep1 dur/tokens %+v", e)
	}
	if len(e.IntermediateArtifacts) != 1 || e.IntermediateArtifacts[0] != "/tmp/helper.py" {
		t.Fatalf("artifacts %v", e.IntermediateArtifacts)
	}
	if len(e.FilesRead) != 2 || e.FilesRead[0] != "/a" {
		t.Fatalf("files %v", e.FilesRead)
	}
	e = eps[1]
	if e.Ended || e.SubagentCount != 1 || e.DurationMs != 3000 || e.UserPromptExcerpt != "more" {
		t.Fatalf("ep2 %+v", e)
	}
	b := Buckets()
	for _, e := range eps {
		AddToBuckets(b, e.ToolCallCount)
	}
	if b[1].Count != 2 {
		t.Fatalf("buckets %+v", b)
	}
}

func TestExcerptRuneSafe(t *testing.T) {
	s := "héllo"
	if got := Excerpt(s, 2); got != "h…" {
		t.Fatalf("got %q", got)
	}
}
