package digest

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/geekychris/claude-session-analyzer/pkg/analyze"
	"github.com/geekychris/claude-session-analyzer/pkg/parse"
)

func basic(t *testing.T) analyze.SessionAnalysis {
	f, err := os.Open("../analyze/testdata/basic.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	recs, err := parse.Records(f)
	if err != nil {
		t.Fatal(err)
	}
	return analyze.Session(recs, analyze.Options{})
}

func TestBuildContents(t *testing.T) {
	d := Build(basic(t), Options{Title: "Run 1, stage implement"})
	t.Log("\n" + d)
	for _, want := range []string{
		"Run 1, stage implement",
		"Models: claude-sonnet-4-5",
		"Ask: Add a greeting function to hello.go and test it",
		"Tools before first edit: 2",
		"Edited: /work/demo/hello.go, /work/demo/hello_test.go",
		"Errors: 1 (retries 1)",
		"FAIL: TestGreet",
		"Commands: go test",
		"Sub-agents: 1 calls (e.g. Survey test layout)",
		"FINAL OUTCOME",
		"Tests live next to the code",
		"wall 22m00s, active 2m00s",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("digest lacks %q:\n%s", want, d)
		}
	}
	if strings.Contains(d, "secret thoughts") {
		t.Fatal("thinking leaked")
	}
}

// big makes a session with many long episodes.
func big(n int) analyze.SessionAnalysis {
	var recs []parse.Record
	for i := 0; i < n; i++ {
		ts := fmt.Sprintf("2026-11-01T10:%02d:%02dZ", (i/60)%60, i%60)
		prompt, _ := json.Marshal(fmt.Sprintf("ask %d: %s", i, strings.Repeat("please refactor the widget ", 40)))
		recs = append(recs, parse.Record{Type: "user", Timestamp: ts, Message: &parse.MessagePayload{Role: "user", Content: prompt}})
		content, _ := json.Marshal([]map[string]any{
			{"type": "text", "text": strings.Repeat("I will now explain at length. ", 30)},
			{"type": "tool_use", "id": fmt.Sprintf("t%d", i), "name": "Edit", "input": map[string]string{"file_path": fmt.Sprintf("/src/file%d.go", i)}},
		})
		recs = append(recs, parse.Record{Type: "assistant", Timestamp: ts, Message: &parse.MessagePayload{
			Role: "assistant", Model: "claude-sonnet-4-5", ID: fmt.Sprintf("m%d", i), Content: content,
			Usage: &parse.Usage{InputTokens: 10, OutputTokens: 10}}})
	}
	return analyze.Session(recs, analyze.Options{})
}

func TestBuildRespectsBudget(t *testing.T) {
	for _, n := range []int{1, 10, 200, 2000} {
		a := big(n)
		for _, budget := range []int{600, 4000, 16000} {
			d := Build(a, Options{MaxBytes: budget})
			if len(d) > budget {
				t.Fatalf("n=%d budget=%d: len %d", n, budget, len(d))
			}
			if !utf8.ValidString(d) {
				t.Fatalf("n=%d budget=%d: invalid utf-8", n, budget)
			}
			if !strings.HasPrefix(d, "SESSION") {
				t.Fatalf("header lost: %q", d[:40])
			}
		}
	}
	// Elision keeps the first and last episode.
	d := Build(big(500), Options{MaxBytes: 8000})
	if !strings.Contains(d, "#1 ") || !strings.Contains(d, "#500 ") || !strings.Contains(d, "episodes omitted") {
		t.Fatalf("elision lost ends:\n%s", d)
	}
}

func TestTruncate(t *testing.T) {
	s := strings.Repeat("é", 100)
	got := Truncate(s, 51)
	if len(got) > 51 || !utf8.ValidString(got) {
		t.Fatalf("%d %q", len(got), got)
	}
	if Truncate("short", 10) != "short" {
		t.Fatal("short string changed")
	}
}

func TestDur(t *testing.T) {
	for in, want := range map[float64]string{12: "12s", 250: "4m10s", 3720: "1h02m"} {
		if got := Dur(in); got != want {
			t.Errorf("Dur(%v)=%s want %s", in, got, want)
		}
	}
}
