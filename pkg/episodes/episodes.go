// Package episodes segments a session into episodes, without any storage.
//
// An episode spans from a user turn (a user message that isn't just a
// tool_result carrier) to the first file-modifying tool call in the ensuing
// assistant chain. "Before edit" metrics (tool count, bytes read) stop at the
// first edit; membership (ToolCallIDs, bash/search/sub-agent counts) runs until
// the next user turn. If no edit fires the episode has Ended=false.
package episodes

import (
	"sort"
	"strings"
	"time"
)

// Message is the minimal view of one message. IDs are caller-chosen (a DB
// row id, or an index) and only need to be unique within the session.
type Message struct {
	ID                  int64
	Role                string // user, assistant, system, meta
	Subtype             string // "tool_result" marks a user tool_result carrier
	Ts                  string // RFC 3339
	Text                string
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
}

// ToolCall is the minimal view of one tool call, attributed to MessageID.
type ToolCall struct {
	ID          int64
	MessageID   int64
	Name        string
	FilePath    string
	IsModifying bool
	IsRead      bool
	IsBash      bool
	IsSearch    bool
	IsSubagent  bool
	Ts          string
	ResultSize  int64
}

// Episode is one derived episode. Seq starts at 1.
type Episode struct {
	Seq                 int      `json:"seq"`
	UserMessageID       int64    `json:"userMessageId"`
	FirstEditToolCallID int64    `json:"firstEditToolCallId"` // 0 when no edit fired
	Ended               bool     `json:"ended"`               // an edit fired
	UserPromptExcerpt   string   `json:"userPromptExcerpt"`
	StartedAt           string   `json:"startedAt"`
	FirstEditAt         string   `json:"firstEditAt"`
	LastAt              string   `json:"lastAt"`
	DurationMs          int64    `json:"durationMs"`    // to the first edit, or to the last activity
	ToolCallCount       int      `json:"toolCallCount"` // tools before the first edit
	FilesRead           []string `json:"filesRead"`
	BashCount           int      `json:"bashCount"`
	SearchCount         int      `json:"searchCount"`
	SubagentCount       int      `json:"subagentCount"`
	InputTokens         int64    `json:"inputTokens"`
	OutputTokens        int64    `json:"outputTokens"`
	CacheReadTokens     int64    `json:"cacheReadTokens"`
	CacheCreationTokens int64    `json:"cacheCreationTokens"`
	EditFilePath        string   `json:"editFilePath"`
	ToolCallIDs         []int64  `json:"toolCallIds"`

	BytesReadBeforeEdit   int64    `json:"bytesReadBeforeEdit"`
	IntermediateArtifacts []string `json:"intermediateArtifacts"`
}

// ExcerptLen is the length of UserPromptExcerpt.
const ExcerptLen = 240

// Derive segments msgs (in session order) with their tool calls. cwd is the
// session's working directory (currently unused by the artifact rule, kept
// for future heuristics).
func Derive(msgs []Message, tcs []ToolCall, cwd string) []Episode {
	tcByMsg := make(map[int64][]ToolCall, len(tcs))
	for _, tc := range tcs {
		tcByMsg[tc.MessageID] = append(tcByMsg[tc.MessageID], tc)
	}
	var out []Episode
	var cur *accum
	flush := func() {
		if cur == nil {
			return
		}
		e := cur.finalize()
		e.Seq = len(out) + 1
		out = append(out, e)
		cur = nil
	}
	for _, m := range msgs {
		if IsUserTurn(m.Role, m.Subtype) {
			flush()
			cur = newAccum(m, cwd)
			continue
		}
		if cur == nil {
			continue
		}
		if m.Role == "assistant" {
			cur.addTokens(m)
		}
		for _, tc := range tcByMsg[m.ID] {
			cur.addToolCall(tc)
		}
	}
	flush()
	return out
}

// IsUserTurn reports whether a message starts an episode.
func IsUserTurn(role, subtype string) bool {
	return role == "user" && subtype != "tool_result"
}

type accum struct {
	projectCWD          string
	userMessageID       int64
	userPromptExcerpt   string
	startedAt           string
	lastTs              string
	toolCallCount       int
	filesRead           map[string]struct{}
	bashCount           int
	searchCount         int
	subagentCount       int
	inputTokens         int64
	outputTokens        int64
	cacheReadTokens     int64
	cacheCreationTokens int64
	toolCallIDs         []int64
	bytesReadBeforeEdit int64
	artifacts           map[string]struct{}
	editSeen            bool
	firstEditTC         ToolCall
}

func newAccum(m Message, cwd string) *accum {
	return &accum{
		projectCWD:        cwd,
		userMessageID:     m.ID,
		userPromptExcerpt: Excerpt(m.Text, ExcerptLen),
		startedAt:         m.Ts,
		lastTs:            m.Ts,
		filesRead:         map[string]struct{}{},
		artifacts:         map[string]struct{}{},
	}
}

func (a *accum) addTokens(m Message) {
	a.inputTokens += m.InputTokens
	a.outputTokens += m.OutputTokens
	a.cacheReadTokens += m.CacheReadTokens
	a.cacheCreationTokens += m.CacheCreationTokens
	if m.Ts != "" {
		a.lastTs = m.Ts
	}
}

func (a *accum) addToolCall(tc ToolCall) {
	a.toolCallIDs = append(a.toolCallIDs, tc.ID)
	if tc.IsBash {
		a.bashCount++
	}
	if tc.IsSearch {
		a.searchCount++
	}
	if tc.IsSubagent {
		a.subagentCount++
	}
	if (tc.IsRead || tc.IsModifying) && tc.FilePath != "" {
		a.filesRead[tc.FilePath] = struct{}{}
	}
	if tc.Ts != "" {
		a.lastTs = tc.Ts
	}
	if tc.Name == "Write" && tc.FilePath != "" && IsIntermediateArtifact(tc.FilePath) {
		a.artifacts[tc.FilePath] = struct{}{}
	}
	if tc.IsModifying {
		if !a.editSeen {
			a.editSeen = true
			a.firstEditTC = tc
		}
		return
	}
	if !a.editSeen {
		a.toolCallCount++
		if tc.IsRead || tc.IsSearch {
			a.bytesReadBeforeEdit += tc.ResultSize
		}
	}
}

// IsIntermediateArtifact classifies a Write as a scratch/helper file: anything
// under /tmp, /private/tmp, /var/tmp or /var/folders. Writes to sibling repos
// are deliberately not flagged.
func IsIntermediateArtifact(path string) bool {
	for _, p := range []string{"/tmp/", "/private/tmp/", "/var/tmp/", "/var/folders/"} {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

func (a *accum) finalize() Episode {
	files := sortedKeys(a.filesRead)
	artifacts := sortedKeys(a.artifacts)
	e := Episode{
		UserMessageID:         a.userMessageID,
		Ended:                 a.editSeen,
		UserPromptExcerpt:     a.userPromptExcerpt,
		StartedAt:             a.startedAt,
		LastAt:                a.lastTs,
		ToolCallCount:         a.toolCallCount,
		FilesRead:             files,
		BashCount:             a.bashCount,
		SearchCount:           a.searchCount,
		SubagentCount:         a.subagentCount,
		InputTokens:           a.inputTokens,
		OutputTokens:          a.outputTokens,
		CacheReadTokens:       a.cacheReadTokens,
		CacheCreationTokens:   a.cacheCreationTokens,
		ToolCallIDs:           a.toolCallIDs,
		BytesReadBeforeEdit:   a.bytesReadBeforeEdit,
		IntermediateArtifacts: artifacts,
	}
	if a.editSeen {
		e.FirstEditAt = a.firstEditTC.Ts
		e.EditFilePath = a.firstEditTC.FilePath
		e.FirstEditToolCallID = a.firstEditTC.ID
		e.DurationMs = TsDeltaMs(a.startedAt, e.FirstEditAt)
	} else {
		e.DurationMs = TsDeltaMs(a.startedAt, a.lastTs)
	}
	return e
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Excerpt cuts s to n bytes (on a rune boundary) and appends an ellipsis.
func Excerpt(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// TsDeltaMs is b-a in milliseconds for RFC 3339 timestamps; 0 if either is
// missing or unparsable.
func TsDeltaMs(a, b string) int64 {
	if a == "" || b == "" {
		return 0
	}
	ta, err1 := time.Parse(time.RFC3339Nano, a)
	tb, err2 := time.Parse(time.RFC3339Nano, b)
	if err1 != nil || err2 != nil {
		return 0
	}
	return tb.Sub(ta).Milliseconds()
}

// Bucket is one bin of the tools-before-edit distribution.
type Bucket struct {
	Label string `json:"label"`
	Min   int    `json:"min"`
	Max   int    `json:"max"`
	Count int    `json:"count"`
}

// Buckets returns the standard empty bins (0, 1-5, 6-20, 21-50, 51-100, 100+).
func Buckets() []Bucket {
	return []Bucket{
		{Label: "0", Min: 0, Max: 0},
		{Label: "1-5", Min: 1, Max: 5},
		{Label: "6-20", Min: 6, Max: 20},
		{Label: "21-50", Min: 21, Max: 50},
		{Label: "51-100", Min: 51, Max: 100},
		{Label: "100+", Min: 101, Max: 1 << 30},
	}
}

// AddToBuckets counts n into the matching bin.
func AddToBuckets(b []Bucket, n int) {
	for i := range b {
		if n >= b[i].Min && n <= b[i].Max {
			b[i].Count++
			return
		}
	}
}
