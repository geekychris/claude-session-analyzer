package parse

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func TestRecordsTolerant(t *testing.T) {
	f, err := os.Open("../analyze/testdata/basic.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	recs, st, err := RecordsWithStats(f)
	if err != nil {
		t.Fatal(err)
	}
	if st.Malformed != 1 {
		t.Errorf("malformed = %d, want 1", st.Malformed)
	}
	if !st.Partial {
		t.Errorf("partial final line not reported")
	}
	if len(recs) != 26 {
		t.Fatalf("records = %d, want 26", len(recs))
	}
	last := recs[len(recs)-1]
	if last.UUID != "u3" {
		t.Errorf("final partial line not kept: %q", last.UUID)
	}
	var unknown bool
	for _, r := range recs {
		if r.Type == "some-future-record" {
			unknown = true
			if len(r.Raw) == 0 {
				t.Errorf("unknown record lost its raw JSON")
			}
		}
	}
	if !unknown {
		t.Errorf("unknown record type dropped")
	}
}

func TestRecordsWrongShapeKept(t *testing.T) {
	// "message" is a string, not an object: the field is skipped, the record kept.
	in := `{"type":"user","uuid":"x","message":"oops"}` + "\n" + "\n" + `{"type":"system","subtype":"turn_duration"}` + "\n"
	recs, err := Records(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[0].UUID != "x" || recs[1].Subtype != "turn_duration" {
		t.Fatalf("got %+v", recs)
	}
}

func TestReaderOffsets(t *testing.T) {
	l1 := `{"type":"user","uuid":"a"}` + "\n"
	l2 := `{"type":"user","uuid":"b"}` + "\n"
	rd := NewReader(strings.NewReader(l1+l2), 1000)
	r, err := rd.Next()
	if err != nil || r.Offset != 1000 {
		t.Fatalf("first: %v %v", r, err)
	}
	r, err = rd.Next()
	if err != nil || r.Offset != 1000+int64(len(l1)) {
		t.Fatalf("second: %v %v", r, err)
	}
	if _, err := rd.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("want EOF, got %v", err)
	}
	if rd.Offset() != 1000+int64(len(l1)+len(l2)) {
		t.Fatalf("offset = %d", rd.Offset())
	}
}

func TestNormalize(t *testing.T) {
	rec, err := ParseLine([]byte(`{"type":"assistant","message":{"role":"assistant","id":"m","model":"claude-x","content":[{"type":"thinking","thinking":"hidden"},{"type":"text","text":"hi"},{"type":"tool_use","id":"t","name":"Read","input":{"file_path":"/a"}}],"usage":{"input_tokens":3,"output_tokens":4}}}`))
	if err != nil {
		t.Fatal(err)
	}
	m := Normalize(rec)
	if m.Role != "assistant" || m.Model != "claude-x" || m.MessageID != "m" || m.Usage.OutputTokens != 4 {
		t.Fatalf("%+v", m)
	}
	if m.Text != "hi\n[tool_use Read]" || m.AssistantText != "hi" || len(m.ToolUses) != 1 {
		t.Fatalf("text %q / %q", m.Text, m.AssistantText)
	}
	if strings.Contains(m.Text, "hidden") {
		t.Fatal("thinking leaked")
	}
	rec, _ = ParseLine([]byte(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","is_error":true,"content":[{"type":"text","text":"boom"}]}]}}`))
	m = Normalize(rec)
	if m.Subtype != "tool_result" || len(m.ToolResults) != 1 || !m.ToolResults[0].IsError || m.Text != "boom" {
		t.Fatalf("%+v", m)
	}
	m = Normalize(Record{Type: "file-history-snapshot"})
	if m.Role != "meta" || m.Subtype != "file-history-snapshot" {
		t.Fatalf("%+v", m)
	}
}

func TestMergeByTime(t *testing.T) {
	a := []Record{{UUID: "a1", Timestamp: "2026-01-01T00:00:01Z"}, {UUID: "a2"}, {UUID: "a3", Timestamp: "2026-01-01T00:00:05Z"}}
	b := []Record{{UUID: "b1", Timestamp: "2026-01-01T00:00:02Z"}, {UUID: "b2", Timestamp: "2026-01-01T00:00:03Z"}}
	var got []string
	for _, r := range MergeByTime(a, b) {
		got = append(got, r.UUID)
	}
	if strings.Join(got, ",") != "a1,a2,b1,b2,a3" {
		t.Fatalf("got %v", got)
	}
}
