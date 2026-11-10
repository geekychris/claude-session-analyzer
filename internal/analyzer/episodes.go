// Package analyzer derives episodes for a stored session. The segmentation
// itself lives in pkg/episodes (storage-free); this package loads rows from
// the store, calls it, and writes the result back.
package analyzer

import (
	"database/sql"

	"github.com/geekychris/claude-session-analyzer/internal/store"
	"github.com/geekychris/claude-session-analyzer/pkg/episodes"
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
	cwd := lookupSessionCWD(a.Store.DB(), sessionID)
	eps := episodes.Derive(msgs, tcs, cwd)

	tx, err := a.Store.DB().Begin()
	if err != nil {
		return 0, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	for i, e := range eps {
		if _, err := a.Store.InsertEpisode(tx, store.EpisodeInsert{
			SessionID:             sessionID,
			Seq:                   e.Seq,
			UserMessageID:         e.UserMessageID,
			FirstEditToolCallID:   e.FirstEditToolCallID,
			Ended:                 e.Ended,
			UserPromptExcerpt:     e.UserPromptExcerpt,
			StartedAt:             e.StartedAt,
			FirstEditAt:           e.FirstEditAt,
			DurationMs:            e.DurationMs,
			ToolCallCount:         e.ToolCallCount,
			FilesReadCount:        len(e.FilesRead),
			FilesRead:             e.FilesRead,
			BashCount:             e.BashCount,
			SearchCount:           e.SearchCount,
			SubagentCount:         e.SubagentCount,
			InputTokens:           e.InputTokens,
			OutputTokens:          e.OutputTokens,
			CacheReadTokens:       e.CacheReadTokens,
			CacheCreationTokens:   e.CacheCreationTokens,
			EditFilePath:          e.EditFilePath,
			ToolCallIDs:           e.ToolCallIDs,
			BytesReadBeforeEdit:   e.BytesReadBeforeEdit,
			IntermediateArtifacts: e.IntermediateArtifacts,
		}); err != nil {
			return i, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	tx = nil
	return len(eps), nil
}

func loadMessages(db *sql.DB, sessionID string) ([]episodes.Message, error) {
	rows, err := db.Query(`
		SELECT id, role, COALESCE(event_subtype,''),
		       COALESCE(ts,''), COALESCE(text_content,''),
		       COALESCE(input_tokens,0), COALESCE(output_tokens,0),
		       COALESCE(cache_read_tokens,0), COALESCE(cache_creation_tokens,0)
		FROM messages WHERE session_id = ? ORDER BY seq
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []episodes.Message
	for rows.Next() {
		var m episodes.Message
		if err := rows.Scan(&m.ID, &m.Role, &m.Subtype, &m.Ts, &m.Text,
			&m.InputTokens, &m.OutputTokens,
			&m.CacheReadTokens, &m.CacheCreationTokens); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func loadToolCalls(db *sql.DB, sessionID string) ([]episodes.ToolCall, error) {
	rows, err := db.Query(`
		SELECT id, message_id, name, COALESCE(file_path,''),
		       is_modifying, is_read, is_bash, is_search, is_subagent, COALESCE(ts,''),
		       COALESCE(result_size,0)
		FROM tool_calls WHERE session_id = ? ORDER BY seq
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []episodes.ToolCall
	for rows.Next() {
		var tc episodes.ToolCall
		var mod, rd, bash, srch, sub int
		if err := rows.Scan(&tc.ID, &tc.MessageID, &tc.Name, &tc.FilePath,
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
