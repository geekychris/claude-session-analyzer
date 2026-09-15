// Package analyzer derives episodes (and other per-session metrics) from the
// raw messages and tool_calls that the ingester wrote.
//
// Episode definition
//
//   An episode spans from a user turn (a user message that isn't just a
//   tool_result carrier) to the first file-modifying tool_call in the ensuing
//   assistant chain. If the session ends before an edit fires, the trailing
//   episode is recorded as `ended=false`. This is the "context building before
//   code is written" unit the user cares about.
package analyzer

import (
	"database/sql"
	"strings"
	"time"

	"github.com/geekychris/claude-session-analyzer/internal/store"
)

type Analyzer struct {
	Store *store.Store
}

// RebuildSession wipes existing episodes for the session and recomputes from
// scratch. Returns the number of episodes written.
func (a *Analyzer) RebuildSession(sessionID string) (int, error) {
	if err := a.Store.DeleteEpisodesForSession(sessionID); err != nil {
		return 0, err
	}

	msgs, err := loadMessages(a.Store.DB(), sessionID)
	if err != nil {
		return 0, err
	}
	tcs, err := loadToolCalls(a.Store.DB(), sessionID)
	if err != nil {
		return 0, err
	}

	// Project cwd is the "canonical" workspace root. Anything written outside
	// it counts as an intermediate artifact (helper script, scratch file).
	cwd := lookupSessionCWD(a.Store.DB(), sessionID)

	// Group tool_calls by message_id so we can attribute them to an in-flight episode.
	tcByMsg := make(map[int64][]toolRow, len(tcs))
	for _, tc := range tcs {
		tcByMsg[tc.MessageID] = append(tcByMsg[tc.MessageID], tc)
	}

	db := a.Store.DB()
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	var episodes int
	var current *episodeAccum
	seq := 1

	flush := func() error {
		if current == nil {
			return nil
		}
		e := current.finalize()
		e.Seq = seq
		if _, err := a.Store.InsertEpisode(tx, e); err != nil {
			return err
		}
		seq++
		episodes++
		current = nil
		return nil
	}

	for _, m := range msgs {
		// A "real" user turn starts a new episode. Skip user messages that
		// only carry tool_results, and skip meta/system rows.
		if m.Role == "user" && m.EventSubtype != "tool_result" {
			if err := flush(); err != nil {
				return episodes, err
			}
			current = newEpisodeAccum(sessionID, m, cwd)
			continue
		}
		if current == nil {
			continue // still before any real user turn
		}

		// Accumulate assistant tokens (user tool_result messages carry no usage).
		if m.Role == "assistant" {
			current.addTokens(m)
		}
		// Attribute every tool_call in this message to the current turn's
		// episode. Metrics that care about "before edit" (tool_call_count,
		// bytes_read_before_edit, files_read) stop accumulating once the first
		// modifying call fires — but the raw tool_call → episode link keeps
		// growing until the next user turn. That way blast-radius queries can
		// count all files edited in a turn, not just the first one.
		for _, tc := range tcByMsg[m.ID] {
			current.addToolCall(tc)
		}
	}

	if err := flush(); err != nil {
		return episodes, err
	}

	if err := tx.Commit(); err != nil {
		return episodes, err
	}
	tx = nil
	return episodes, nil
}

// ---------------------------------------------------------------------------
// internal shapes
// ---------------------------------------------------------------------------

type msgRow struct {
	ID                  int64
	Seq                 int
	UUID                string
	Role                string
	EventType           string
	EventSubtype        string
	Ts                  string
	TextContent         string
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
}

type toolRow struct {
	ID          int64
	MessageID   int64
	Seq         int
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

func loadMessages(db *sql.DB, sessionID string) ([]msgRow, error) {
	rows, err := db.Query(`
		SELECT id, seq, COALESCE(uuid,''), role, event_type, COALESCE(event_subtype,''),
		       COALESCE(ts,''), COALESCE(text_content,''),
		       COALESCE(input_tokens,0), COALESCE(output_tokens,0),
		       COALESCE(cache_read_tokens,0), COALESCE(cache_creation_tokens,0)
		FROM messages WHERE session_id = ? ORDER BY seq
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []msgRow
	for rows.Next() {
		var m msgRow
		if err := rows.Scan(&m.ID, &m.Seq, &m.UUID, &m.Role, &m.EventType, &m.EventSubtype,
			&m.Ts, &m.TextContent,
			&m.InputTokens, &m.OutputTokens,
			&m.CacheReadTokens, &m.CacheCreationTokens); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func loadToolCalls(db *sql.DB, sessionID string) ([]toolRow, error) {
	rows, err := db.Query(`
		SELECT id, message_id, seq, name, COALESCE(file_path,''),
		       is_modifying, is_read, is_bash, is_search, is_subagent, COALESCE(ts,''),
		       COALESCE(result_size,0)
		FROM tool_calls WHERE session_id = ? ORDER BY seq
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []toolRow
	for rows.Next() {
		var tc toolRow
		var mod, rd, bash, srch, sub int
		if err := rows.Scan(&tc.ID, &tc.MessageID, &tc.Seq, &tc.Name, &tc.FilePath,
			&mod, &rd, &bash, &srch, &sub, &tc.Ts, &tc.ResultSize); err != nil {
			return nil, err
		}
		tc.IsModifying = mod == 1
		tc.IsRead = rd == 1
		tc.IsBash = bash == 1
		tc.IsSearch = srch == 1
		tc.IsSubagent = sub == 1
		out = append(out, tc)
	}
	return out, rows.Err()
}

func lookupSessionCWD(db *sql.DB, sessionID string) string {
	var cwd string
	_ = db.QueryRow(`SELECT COALESCE(cwd,'') FROM sessions WHERE session_id = ?`, sessionID).Scan(&cwd)
	return cwd
}

// episodeAccum accumulates state for the currently-open episode.
type episodeAccum struct {
	sessionID           string
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

	// First modifying tool_call seen this turn — closes "before edit" metrics
	// while attribution keeps going for follow-up work.
	editSeen       bool
	firstEditTC    toolRow
	firstEditTCSet bool
}

func newEpisodeAccum(sessionID string, m msgRow, cwd string) *episodeAccum {
	return &episodeAccum{
		sessionID:         sessionID,
		projectCWD:        cwd,
		userMessageID:     m.ID,
		userPromptExcerpt: excerpt(m.TextContent, 240),
		startedAt:         m.Ts,
		lastTs:            m.Ts,
		filesRead:         map[string]struct{}{},
		artifacts:         map[string]struct{}{},
	}
}

func (a *episodeAccum) addTokens(m msgRow) {
	a.inputTokens += m.InputTokens
	a.outputTokens += m.OutputTokens
	a.cacheReadTokens += m.CacheReadTokens
	a.cacheCreationTokens += m.CacheCreationTokens
	if m.Ts != "" {
		a.lastTs = m.Ts
	}
}

// addToolCall attributes a tool_call to this episode. Metrics that are only
// meaningful *before* the first modifying tool call (tool_call_count,
// bytes_read_before_edit) stop growing once editSeen flips true; the raw
// tool_call → episode association keeps growing until the next user turn.
func (a *episodeAccum) addToolCall(tc toolRow) {
	a.toolCallIDs = append(a.toolCallIDs, tc.ID)

	// Bash / search / subagent counts run for the whole turn — they describe
	// activity level, not context depth.
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
	if tc.Name == "Write" && tc.FilePath != "" && isIntermediateArtifact(tc.FilePath, a.projectCWD) {
		a.artifacts[tc.FilePath] = struct{}{}
	}

	if tc.IsModifying {
		if !a.editSeen {
			a.editSeen = true
			a.firstEditTC = tc
			a.firstEditTCSet = true
		}
		return
	}

	// Pre-edit metrics only accumulate while editSeen is false.
	if !a.editSeen {
		a.toolCallCount++
		if tc.IsRead || tc.IsSearch {
			a.bytesReadBeforeEdit += tc.ResultSize
		}
	}
}

// isIntermediateArtifact classifies a Write as a scratch/helper script:
// anything under /tmp, /private/tmp, /var/tmp, or /var/folders. These are the
// dirs Claude uses for one-off python/bash helpers it builds mid-episode. We
// intentionally do NOT flag writes to sibling repos — those are usually
// deliberate cross-repo edits, not throwaway scaffolding.
func isIntermediateArtifact(path, _ string) bool {
	scratchPrefixes := []string{"/tmp/", "/private/tmp/", "/var/tmp/", "/var/folders/"}
	for _, p := range scratchPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

func (a *episodeAccum) finalize() store.EpisodeInsert {
	files := make([]string, 0, len(a.filesRead))
	for f := range a.filesRead {
		files = append(files, f)
	}
	artifacts := make([]string, 0, len(a.artifacts))
	for f := range a.artifacts {
		artifacts = append(artifacts, f)
	}

	firstEditAt := ""
	editPath := ""
	var editID int64
	var dur int64
	if a.firstEditTCSet {
		firstEditAt = a.firstEditTC.Ts
		editPath = a.firstEditTC.FilePath
		editID = a.firstEditTC.ID
		dur = tsDeltaMs(a.startedAt, firstEditAt)
	} else {
		dur = tsDeltaMs(a.startedAt, a.lastTs)
	}

	return store.EpisodeInsert{
		SessionID:             a.sessionID,
		UserMessageID:         a.userMessageID,
		FirstEditToolCallID:   editID,
		Ended:                 a.editSeen,
		UserPromptExcerpt:     a.userPromptExcerpt,
		StartedAt:             a.startedAt,
		FirstEditAt:           firstEditAt,
		DurationMs:            dur,
		ToolCallCount:         a.toolCallCount,
		FilesReadCount:        len(files),
		FilesRead:             files,
		BashCount:             a.bashCount,
		SearchCount:           a.searchCount,
		SubagentCount:         a.subagentCount,
		InputTokens:           a.inputTokens,
		OutputTokens:          a.outputTokens,
		CacheReadTokens:       a.cacheReadTokens,
		CacheCreationTokens:   a.cacheCreationTokens,
		EditFilePath:          editPath,
		ToolCallIDs:           a.toolCallIDs,
		BytesReadBeforeEdit:   a.bytesReadBeforeEdit,
		IntermediateArtifacts: artifacts,
	}
}

func excerpt(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func tsDeltaMs(a, b string) int64 {
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
