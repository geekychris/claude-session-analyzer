// Package analyze turns the records of one Claude Code session into a
// SessionAnalysis: messages, classified tool calls, episodes, files touched,
// tokens and estimated cost by model, sub-agent activity, errors and retries,
// and timings. It needs no database and no files.
//
//	recs, _ := parse.Records(r)
//	a := analyze.Session(recs, analyze.Options{})
package analyze

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/geekychris/claude-session-analyzer/pkg/episodes"
	"github.com/geekychris/claude-session-analyzer/pkg/parse"
	"github.com/geekychris/claude-session-analyzer/pkg/pricing"
	"github.com/geekychris/claude-session-analyzer/pkg/toolclass"
)

// Options tune the analysis. The zero value is fine.
type Options struct {
	// IdleGap: gaps between consecutive records longer than this do not count
	// toward ActiveSeconds. Default 5 minutes.
	IdleGap time.Duration
	// MaxTextLen bounds Message.Text (bytes). Default 4000; negative = no limit.
	MaxTextLen int
	// MaxPromptLen bounds Episode.UserPrompt. Default 2000.
	MaxPromptLen int
}

func (o Options) withDefaults() Options {
	if o.IdleGap <= 0 {
		o.IdleGap = 5 * time.Minute
	}
	if o.MaxTextLen == 0 {
		o.MaxTextLen = 4000
	}
	if o.MaxPromptLen <= 0 {
		o.MaxPromptLen = 2000
	}
	return o
}

// Tokens are token counts in the four billing buckets.
type Tokens struct {
	Input         int64 `json:"input"`
	Output        int64 `json:"output"`
	CacheRead     int64 `json:"cacheRead"`
	CacheCreation int64 `json:"cacheCreation"`
}

// Add sums o into t.
func (t *Tokens) Add(o Tokens) {
	t.Input += o.Input
	t.Output += o.Output
	t.CacheRead += o.CacheRead
	t.CacheCreation += o.CacheCreation
}

// Total is the sum of all four buckets.
func (t Tokens) Total() int64 { return t.Input + t.Output + t.CacheRead + t.CacheCreation }

// Cost estimates USD for these tokens at model's list rates.
func (t Tokens) Cost(model string) float64 {
	return pricing.CostUSD(model, t.Input, t.Output, t.CacheRead, t.CacheCreation)
}

// ModelUsage is the usage of one model in a session.
type ModelUsage struct {
	Model    string  `json:"model"`
	Tokens   Tokens  `json:"tokens"`
	CostUSD  float64 `json:"costUsd"`
	Messages int     `json:"messages"` // distinct API responses
}

// Message is one record, flattened.
type Message struct {
	Index       int    `json:"index"`
	UUID        string `json:"uuid,omitempty"`
	Ts          string `json:"ts,omitempty"`
	Role        string `json:"role"`
	Subtype     string `json:"subtype,omitempty"`
	Model       string `json:"model,omitempty"`
	Text        string `json:"text,omitempty"`
	Truncated   bool   `json:"truncated,omitempty"`
	IsSidechain bool   `json:"isSidechain,omitempty"`
	IsMeta      bool   `json:"isMeta,omitempty"`
	// Tokens are counted once per API response (Claude Code writes one record
	// per content block, repeating the usage); later records of the same
	// response carry zero.
	Tokens    Tokens `json:"tokens"`
	ToolCalls []int  `json:"toolCalls,omitempty"` // indexes into SessionAnalysis.ToolCalls
	Episode   int    `json:"episode"`             // episode Seq; 0 before the first user turn
}

// ToolCall is one tool_use with its classification and result.
type ToolCall struct {
	Index        int    `json:"index"`
	ToolUseID    string `json:"toolUseId,omitempty"`
	MessageIndex int    `json:"messageIndex"`
	Episode      int    `json:"episode"`
	Name         string `json:"name"`
	Category     string `json:"category"`
	FilePath     string `json:"filePath,omitempty"`
	IsModifying  bool   `json:"isModifying,omitempty"`
	IsRead       bool   `json:"isRead,omitempty"`
	IsSearch     bool   `json:"isSearch,omitempty"`
	IsBash       bool   `json:"isBash,omitempty"`
	IsSubagent   bool   `json:"isSubagent,omitempty"`
	BashProgram  string `json:"bashProgram,omitempty"`
	BashAction   string `json:"bashAction,omitempty"`
	// Summary is a one-line description of the input (command, path,
	// pattern, task description), bounded.
	Summary     string `json:"summary,omitempty"`
	Ts          string `json:"ts,omitempty"`
	IsSidechain bool   `json:"isSidechain,omitempty"`
	HasResult   bool   `json:"hasResult,omitempty"`
	ResultSize  int64  `json:"resultSize,omitempty"`
	IsError     bool   `json:"isError,omitempty"`
	ErrorText   string `json:"errorText,omitempty"`
	// IsRetry: the previous call of the same tool in this episode failed.
	IsRetry bool `json:"isRetry,omitempty"`
}

// Episode is an episodes.Episode with the extra detail an analysis view and a
// digest want. ToolCallIDs and UserMessageID are indexes into the analysis.
type Episode struct {
	episodes.Episode
	UserPrompt      string         `json:"userPrompt"`
	ToolsBeforeEdit int            `json:"toolsBeforeEdit"`
	ToolCounts      map[string]int `json:"toolCounts"` // by category, whole episode
	ToolNames       map[string]int `json:"toolNames"`
	FilesEdited     []string       `json:"filesEdited"`
	Errors          int            `json:"errors"`
	Retries         int            `json:"retries"`
	// Statements are the assistant's own text blocks in this episode, in
	// order, each bounded (the first and the last few are kept).
	Statements []string `json:"statements"`
	FinalText  string   `json:"finalText,omitempty"`
	Tokens     Tokens   `json:"tokens"`
	CostUSD    float64  `json:"costUsd"`
}

// FileTouch counts how a file was used.
type FileTouch struct {
	Path  string `json:"path"`
	Reads int    `json:"reads"`
	Edits int    `json:"edits"`
}

// ErrorEvent is a failed tool call or an API/system error.
type ErrorEvent struct {
	Ts        string `json:"ts,omitempty"`
	Kind      string `json:"kind"` // tool_error | api_error
	Tool      string `json:"tool,omitempty"`
	ToolCall  int    `json:"toolCall"` // index, -1 for non-tool errors
	Text      string `json:"text,omitempty"`
	Episode   int    `json:"episode"`
	IsRetried bool   `json:"isRetried,omitempty"`
}

// Subagents summarises sub-agent use.
type Subagents struct {
	Calls             int            `json:"calls"`
	ByName            map[string]int `json:"byName"`
	SidechainMessages int            `json:"sidechainMessages"`
	Tasks             []string       `json:"tasks,omitempty"` // descriptions, bounded
}

// SessionAnalysis is everything derived from one session's records.
type SessionAnalysis struct {
	SessionID string `json:"sessionId,omitempty"`
	CWD       string `json:"cwd,omitempty"`
	GitBranch string `json:"gitBranch,omitempty"`
	Title     string `json:"title,omitempty"`
	Version   string `json:"version,omitempty"`

	FirstTimestamp string  `json:"firstTimestamp,omitempty"`
	LastTimestamp  string  `json:"lastTimestamp,omitempty"`
	WallSeconds    float64 `json:"wallSeconds"`
	ActiveSeconds  float64 `json:"activeSeconds"`
	IdleGapSeconds float64 `json:"idleGapSeconds"`

	Records           int `json:"records"`
	UserTurns         int `json:"userTurns"`
	AssistantMessages int `json:"assistantMessages"` // distinct API responses

	Messages  []Message   `json:"messages"`
	ToolCalls []ToolCall  `json:"toolCalls"`
	Episodes  []Episode   `json:"episodes"`
	Files     []FileTouch `json:"files"`

	Tokens       Tokens       `json:"tokens"`
	Models       []ModelUsage `json:"models"`
	PrimaryModel string       `json:"primaryModel,omitempty"`
	CostUSD      float64      `json:"costUsd"`

	ToolCounts     map[string]int    `json:"toolCounts"` // by category
	ToolNames      map[string]int    `json:"toolNames"`
	EpisodeBuckets []episodes.Bucket `json:"episodeBuckets"`
	Subagents      Subagents         `json:"subagents"`
	Errors         []ErrorEvent      `json:"errors"`
	Retries        int               `json:"retries"`
	FinalText      string            `json:"finalText,omitempty"`
	PRLinks        []string          `json:"prLinks,omitempty"`
}

// Session analyzes one session. Records from several files (a main session
// and its sub-agent transcripts) can be passed together; merge them with
// parse.MergeByTime first so the timeline is in order.
func Session(recs []parse.Record, opts Options) SessionAnalysis {
	o := opts.withDefaults()
	a := SessionAnalysis{
		Records:        len(recs),
		ToolCounts:     map[string]int{},
		ToolNames:      map[string]int{},
		Subagents:      Subagents{ByName: map[string]int{}},
		IdleGapSeconds: o.IdleGap.Seconds(),
		Messages:       []Message{},
		ToolCalls:      []ToolCall{},
		Episodes:       []Episode{},
		Files:          []FileTouch{},
		Errors:         []ErrorEvent{},
	}

	// Usage per API response: keep the max of each bucket over its records.
	respUsage := map[string]parse.Usage{}
	for _, r := range recs {
		if r.Type != "assistant" || r.Message == nil || r.Message.Usage == nil || r.Message.ID == "" {
			continue
		}
		u := respUsage[r.Message.ID]
		mu := *r.Message.Usage
		u.InputTokens = max(u.InputTokens, mu.InputTokens)
		u.OutputTokens = max(u.OutputTokens, mu.OutputTokens)
		u.CacheReadInputTokens = max(u.CacheReadInputTokens, mu.CacheReadInputTokens)
		u.CacheCreationInputTokens = max(u.CacheCreationInputTokens, mu.CacheCreationInputTokens)
		respUsage[r.Message.ID] = u
	}
	seenResp := map[string]bool{}
	byModel := map[string]*ModelUsage{}
	toolByUseID := map[string]int{}
	var times []time.Time
	var epMsgs []episodes.Message
	var epTCs []episodes.ToolCall
	prSeen := map[string]bool{}
	asstText := map[int]string{}
	userText := map[int]string{}

	for i, r := range recs {
		if a.SessionID == "" && r.SessionID != "" {
			a.SessionID = r.SessionID
		}
		if a.CWD == "" && r.CWD != "" {
			a.CWD = r.CWD
		}
		if a.GitBranch == "" && r.GitBranch != "" {
			a.GitBranch = r.GitBranch
		}
		if a.Version == "" && r.Version != "" {
			a.Version = r.Version
		}
		if r.AITitle != "" {
			a.Title = r.AITitle
		}
		if r.PRUrl != "" && !prSeen[r.PRUrl] {
			prSeen[r.PRUrl] = true
			a.PRLinks = append(a.PRLinks, r.PRUrl)
		}
		if t, ok := parseTs(r.Timestamp); ok {
			times = append(times, t)
		}

		nm := parse.Normalize(r)
		m := Message{
			Index: i, UUID: r.UUID, Ts: r.Timestamp, Role: nm.Role, Subtype: nm.Subtype,
			Model: nm.Model, IsSidechain: r.IsSidechain, IsMeta: r.IsMeta,
		}
		m.Text, m.Truncated = bound(nm.Text, o.MaxTextLen)
		if r.IsSidechain {
			a.Subagents.SidechainMessages++
		}

		if nm.Role == "assistant" && nm.AssistantText != "" {
			asstText[i] = nm.AssistantText
		}
		if nm.Role == "user" && nm.Subtype != "tool_result" {
			userText[i] = nm.Text
		}
		if nm.Role == "assistant" {
			var u parse.Usage
			counted := true
			if nm.MessageID != "" {
				if seenResp[nm.MessageID] {
					counted = false
				} else {
					seenResp[nm.MessageID] = true
					u = respUsage[nm.MessageID]
				}
			} else {
				u = nm.Usage
			}
			if counted {
				a.AssistantMessages++
				m.Tokens = Tokens{
					Input: int64(u.InputTokens), Output: int64(u.OutputTokens),
					CacheRead: int64(u.CacheReadInputTokens), CacheCreation: int64(u.CacheCreationInputTokens),
				}
				model := nm.Model
				if model == "" {
					model = "unknown"
				}
				if model != "<synthetic>" {
					mu := byModel[model]
					if mu == nil {
						mu = &ModelUsage{Model: model}
						byModel[model] = mu
					}
					mu.Tokens.Add(m.Tokens)
					mu.Messages++
				}
			}
		}
		if nm.Role == "system" && (nm.Subtype == "api_error" || strings.Contains(nm.Subtype, "error")) {
			txt := nm.Text
			if txt == "" {
				txt = nm.Subtype
			}
			a.Errors = append(a.Errors, ErrorEvent{Ts: r.Timestamp, Kind: "api_error", ToolCall: -1, Text: excerpt(txt, 300)})
		}

		for _, b := range nm.ToolUses {
			tf := toolclass.ClassifyTool(b.Name, b.Input)
			tc := ToolCall{
				Index: len(a.ToolCalls), ToolUseID: b.ID, MessageIndex: i, Name: b.Name,
				Category: tf.Category(), FilePath: tf.FilePath,
				IsModifying: tf.IsModifying, IsRead: tf.IsRead, IsSearch: tf.IsSearch,
				IsBash: tf.IsBash, IsSubagent: tf.IsSubagent,
				Summary: summarizeInput(b.Name, b.Input), Ts: r.Timestamp, IsSidechain: r.IsSidechain,
			}
			if tf.IsBash {
				tc.BashProgram, tc.BashAction = toolclass.ClassifyBashInput(b.Input)
			}
			if tf.IsSubagent {
				a.Subagents.Calls++
				a.Subagents.ByName[b.Name]++
				if (b.Name == "Task" || b.Name == "Agent") && len(a.Subagents.Tasks) < 50 && tc.Summary != "" {
					a.Subagents.Tasks = append(a.Subagents.Tasks, tc.Summary)
				}
			}
			if b.ID != "" {
				toolByUseID[b.ID] = tc.Index
			}
			m.ToolCalls = append(m.ToolCalls, tc.Index)
			a.ToolCalls = append(a.ToolCalls, tc)
			a.ToolCounts[tc.Category]++
			a.ToolNames[tc.Name]++
			epTCs = append(epTCs, episodes.ToolCall{
				ID: int64(tc.Index), MessageID: int64(i), Name: tc.Name, FilePath: tc.FilePath,
				IsModifying: tc.IsModifying, IsRead: tc.IsRead, IsBash: tc.IsBash,
				IsSearch: tc.IsSearch, IsSubagent: tc.IsSubagent, Ts: tc.Ts,
			})
		}
		for _, b := range nm.ToolResults {
			idx, ok := toolByUseID[b.ToolUseID]
			if !ok {
				continue
			}
			tc := &a.ToolCalls[idx]
			tc.HasResult = true
			tc.ResultSize = int64(len(b.ResultContent))
			epTCs[idx].ResultSize = tc.ResultSize
			if b.IsError {
				tc.IsError = true
				tc.ErrorText = excerpt(parse.StringifyResult(b.ResultContent), 300)
			}
		}

		role := nm.Role
		if r.IsSidechain || r.IsMeta {
			// Sub-agent prompts and injected meta text are not user turns.
			if role == "user" && nm.Subtype != "tool_result" {
				role = "user-aux"
			}
		}
		if episodes.IsUserTurn(role, nm.Subtype) {
			a.UserTurns++
		}
		epMsgs = append(epMsgs, episodes.Message{
			ID: int64(i), Role: role, Subtype: nm.Subtype, Ts: r.Timestamp, Text: nm.Text,
			InputTokens: m.Tokens.Input, OutputTokens: m.Tokens.Output,
			CacheReadTokens: m.Tokens.CacheRead, CacheCreationTokens: m.Tokens.CacheCreation,
		})
		a.Messages = append(a.Messages, m)
	}

	// Errors from tool results (in tool-call order).
	for i := range a.ToolCalls {
		if a.ToolCalls[i].IsError {
			a.Errors = append(a.Errors, ErrorEvent{
				Ts: a.ToolCalls[i].Ts, Kind: "tool_error", Tool: a.ToolCalls[i].Name,
				ToolCall: i, Text: a.ToolCalls[i].ErrorText,
			})
		}
	}

	// Models and cost.
	for _, mu := range byModel {
		mu.CostUSD = mu.Tokens.Cost(mu.Model)
		a.Tokens.Add(mu.Tokens)
		a.CostUSD += mu.CostUSD
		a.Models = append(a.Models, *mu)
	}
	sort.Slice(a.Models, func(i, j int) bool {
		if a.Models[i].Tokens.Output != a.Models[j].Tokens.Output {
			return a.Models[i].Tokens.Output > a.Models[j].Tokens.Output
		}
		return a.Models[i].Model < a.Models[j].Model
	})
	if len(a.Models) > 0 {
		a.PrimaryModel = a.Models[0].Model
	}
	if a.Models == nil {
		a.Models = []ModelUsage{}
	}

	// Timings.
	if len(times) > 0 {
		sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
		a.FirstTimestamp = times[0].UTC().Format(time.RFC3339Nano)
		a.LastTimestamp = times[len(times)-1].UTC().Format(time.RFC3339Nano)
		a.WallSeconds = times[len(times)-1].Sub(times[0]).Seconds()
		a.ActiveSeconds = ActiveTime(times, o.IdleGap).Seconds()
	}

	// Episodes.
	a.EpisodeBuckets = episodes.Buckets()
	for _, e := range episodes.Derive(epMsgs, epTCs, a.CWD) {
		ep := Episode{
			Episode:         e,
			ToolsBeforeEdit: e.ToolCallCount,
			ToolCounts:      map[string]int{},
			ToolNames:       map[string]int{},
			FilesEdited:     []string{},
			Statements:      []string{},
		}
		ep.UserPrompt, _ = bound(userText[int(e.UserMessageID)], o.MaxPromptLen)
		edited := map[string]bool{}
		var lastByName = map[string]int{}
		for _, id := range e.ToolCallIDs {
			tc := &a.ToolCalls[id]
			tc.Episode = e.Seq
			ep.ToolCounts[tc.Category]++
			ep.ToolNames[tc.Name]++
			if tc.IsModifying && tc.FilePath != "" && !edited[tc.FilePath] {
				edited[tc.FilePath] = true
				ep.FilesEdited = append(ep.FilesEdited, tc.FilePath)
			}
			if tc.IsError {
				ep.Errors++
			}
			if prev, ok := lastByName[tc.Name]; ok && a.ToolCalls[prev].IsError {
				tc.IsRetry = true
				ep.Retries++
			}
			lastByName[tc.Name] = int(id)
		}
		a.Retries += ep.Retries
		episodes.AddToBuckets(a.EpisodeBuckets, e.ToolCallCount)
		a.Episodes = append(a.Episodes, ep)
	}
	// Message → episode, statements, per-episode tokens and cost.
	cur := 0
	epIdx := map[int64]int{}
	for i, e := range a.Episodes {
		epIdx[e.UserMessageID] = i
	}
	for i := range a.Messages {
		m := &a.Messages[i]
		if k, ok := epIdx[int64(i)]; ok {
			cur = k + 1
		}
		m.Episode = cur
		if cur == 0 {
			continue
		}
		ep := &a.Episodes[cur-1]
		if m.Role == "assistant" {
			ep.Tokens.Add(m.Tokens)
			ep.CostUSD += m.Tokens.Cost(m.Model)
			if !m.IsSidechain {
				if t := strings.TrimSpace(asstText[i]); t != "" {
					ep.Statements = append(ep.Statements, excerpt(t, 600))
					ep.FinalText = excerpt(t, 1500)
					a.FinalText = ep.FinalText
				}
			}
		}
	}
	for i := range a.Episodes {
		a.Episodes[i].Statements = keepEnds(a.Episodes[i].Statements, 3, 3)
	}
	// Mark retried errors.
	for i := range a.Errors {
		if k := a.Errors[i].ToolCall; k >= 0 {
			a.Errors[i].Episode = a.ToolCalls[k].Episode
			for j := k + 1; j < len(a.ToolCalls); j++ {
				if a.ToolCalls[j].Name == a.ToolCalls[k].Name && a.ToolCalls[j].Episode == a.ToolCalls[k].Episode {
					a.Errors[i].IsRetried = a.ToolCalls[j].IsRetry
					break
				}
			}
		}
	}
	sort.SliceStable(a.Errors, func(i, j int) bool { return a.Errors[i].Ts < a.Errors[j].Ts })

	// Files.
	files := map[string]*FileTouch{}
	for _, tc := range a.ToolCalls {
		if tc.FilePath == "" || !(tc.IsRead || tc.IsModifying) {
			continue
		}
		f := files[tc.FilePath]
		if f == nil {
			f = &FileTouch{Path: tc.FilePath}
			files[tc.FilePath] = f
		}
		if tc.IsModifying {
			f.Edits++
		} else {
			f.Reads++
		}
	}
	for _, f := range files {
		a.Files = append(a.Files, *f)
	}
	sort.Slice(a.Files, func(i, j int) bool {
		ti, tj := a.Files[i].Reads+a.Files[i].Edits, a.Files[j].Reads+a.Files[j].Edits
		if a.Files[i].Edits != a.Files[j].Edits {
			return a.Files[i].Edits > a.Files[j].Edits
		}
		if ti != tj {
			return ti > tj
		}
		return a.Files[i].Path < a.Files[j].Path
	})
	return a
}

// ActiveTime sums the gaps between consecutive sorted times that are no
// longer than idleGap.
func ActiveTime(sorted []time.Time, idleGap time.Duration) time.Duration {
	var d time.Duration
	for i := 1; i < len(sorted); i++ {
		g := sorted[i].Sub(sorted[i-1])
		if g > 0 && g <= idleGap {
			d += g
		}
	}
	return d
}

// FilesEdited returns the distinct files edited in the session, in edit order.
func (a SessionAnalysis) FilesEdited() []string {
	seen := map[string]bool{}
	var out []string
	for _, tc := range a.ToolCalls {
		if tc.IsModifying && tc.FilePath != "" && !seen[tc.FilePath] {
			seen[tc.FilePath] = true
			out = append(out, tc.FilePath)
		}
	}
	return out
}

func parseTs(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	return t, err == nil
}

func bound(s string, n int) (string, bool) {
	if n < 0 || len(s) <= n {
		return s, false
	}
	return episodes.Excerpt(s, n), true
}

func excerpt(s string, n int) string {
	s = strings.TrimSpace(s)
	return episodes.Excerpt(s, n)
}

// keepEnds keeps the first `head` and last `tail` items, with a marker
// between them when something was dropped.
func keepEnds(xs []string, head, tail int) []string {
	if len(xs) <= head+tail {
		return xs
	}
	out := append([]string{}, xs[:head]...)
	out = append(out, "…")
	return append(out, xs[len(xs)-tail:]...)
}

// summarizeInput picks the most telling input field for a one-line summary.
func summarizeInput(name string, input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(input, &m) != nil {
		return ""
	}
	keys := []string{"command", "file_path", "notebook_path", "pattern", "description", "prompt", "url", "query", "path", "subject"}
	if name == "Task" || name == "Agent" {
		keys = []string{"description", "subagent_type", "prompt"}
	}
	for _, k := range keys {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			v = strings.Join(strings.Fields(v), " ")
			return episodes.Excerpt(v, 200)
		}
	}
	return ""
}
