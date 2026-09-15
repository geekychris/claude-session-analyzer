// Package store wraps the analyzer's SQLite database.
//
// The store owns the schema, tracks ingestion offsets per file, and exposes
// the queries the Wails frontend calls. Higher-level orchestration (walking
// ~/.claude/projects, running the parser) lives in the ingest package.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/geekychris/claude-session-analyzer/internal/pricing"
)

type Store struct {
	db *sql.DB
}

// Open opens (or creates) the analyzer database at the given path.
func Open(path string) (*Store, error) {
	// modernc.org/sqlite driver name is "sqlite"; enable FK / busy timeout via query params.
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// WAL lets many readers coexist with one writer; sqlite's internal lock
	// serialises the writes for us, so we don't need MaxOpenConns=1 (that would
	// deadlock any code that calls a Store helper mid-transaction). A small pool
	// keeps ingestion humming without letting Go pile up idle connections.
	db.SetMaxOpenConns(8)
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("schema init: %w", err)
	}
	if err := runMigrations(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrations: %w", err)
	}
	return &Store{db: db}, nil
}

// runMigrations applies additive column additions idempotently. SQLite has no
// "ADD COLUMN IF NOT EXISTS" so we ignore duplicate-column errors.
func runMigrations(db *sql.DB) error {
	additions := []string{
		`ALTER TABLE episodes ADD COLUMN bytes_read_before_edit INTEGER`,
		`ALTER TABLE episodes ADD COLUMN intermediate_artifact_count INTEGER`,
		`ALTER TABLE episodes ADD COLUMN intermediate_artifacts_json TEXT`,
		`ALTER TABLE sessions ADD COLUMN primary_model TEXT`,
		`ALTER TABLE tool_calls ADD COLUMN bash_program TEXT`,
		`ALTER TABLE tool_calls ADD COLUMN bash_action TEXT`,
		`CREATE INDEX IF NOT EXISTS idx_tc_bash_program ON tool_calls(bash_program)`,
		`CREATE INDEX IF NOT EXISTS idx_tc_file_path ON tool_calls(file_path)`,
	}
	for _, stmt := range additions {
		if _, err := db.Exec(stmt); err != nil {
			if strings.Contains(err.Error(), "duplicate column") {
				continue
			}
			return err
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

// DB exposes the underlying handle for packages that want to run their own
// transactions (ingest, analyzer). Keep external usage tight.
func (s *Store) DB() *sql.DB { return s.db }

// ------------------------------------------------------------------
// Projects
// ------------------------------------------------------------------

// UpsertProject records a project, updating cwd/display_name if changed.
// cwd may be empty on first sight; we fill it in when we see a message with cwd.
func (s *Store) UpsertProject(slug, cwd string) error {
	name := deriveDisplayName(slug, cwd)
	_, err := s.db.Exec(`
		INSERT INTO projects(slug, cwd, display_name, last_seen_at)
		VALUES(?, ?, ?, ?)
		ON CONFLICT(slug) DO UPDATE SET
		  cwd          = COALESCE(NULLIF(excluded.cwd, ''), projects.cwd),
		  display_name = COALESCE(NULLIF(excluded.display_name, ''), projects.display_name),
		  last_seen_at = excluded.last_seen_at
	`, slug, cwd, name, nowISO())
	return err
}

func deriveDisplayName(slug, cwd string) string {
	if cwd != "" {
		return filepath.Base(cwd)
	}
	// Slug like "-Users-chris-code-claude-world-claude-trace" — take the last segment.
	parts := strings.Split(strings.TrimPrefix(slug, "-"), "-")
	if len(parts) == 0 {
		return slug
	}
	return parts[len(parts)-1]
}

func (s *Store) ListProjects() ([]Project, error) {
	// Session-level rollup first — cost is per-session (each session may use a
	// different model), so summing at that granularity is more accurate than
	// applying one model to the whole project.
	rows, err := s.db.Query(`
		SELECT p.slug, COALESCE(p.cwd,''), COALESCE(p.display_name,''), COALESCE(p.last_seen_at,''),
		       COUNT(DISTINCT s.session_id),
		       (SELECT COUNT(*) FROM files f WHERE f.project_slug = p.slug),
		       COALESCE(SUM(s.message_count),0),
		       COALESCE(SUM(s.tool_call_count),0),
		       COALESCE(SUM(s.edit_count),0),
		       COALESCE(SUM(s.total_input_tokens),0),
		       COALESCE(SUM(s.total_output_tokens),0),
		       COALESCE(SUM(s.total_cache_read_tokens),0),
		       COALESCE(SUM(s.total_cache_creation_tokens),0)
		FROM projects p
		LEFT JOIN sessions s ON s.project_slug = p.slug
		GROUP BY p.slug
		ORDER BY p.last_seen_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.Slug, &p.CWD, &p.DisplayName, &p.LastSeenAt,
			&p.SessionCount, &p.FileCount,
			&p.TotalMessages, &p.TotalToolCalls, &p.TotalEdits,
			&p.TotalInputTokens, &p.TotalOutputTokens,
			&p.TotalCacheReadTokens, &p.TotalCacheCreationTokens); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Compute per-project cost by summing each session's cost (respects the
	// session's dominant model).
	for i := range out {
		cost, err := s.projectCost(out[i].Slug)
		if err != nil {
			continue
		}
		out[i].EstimatedCostUSD = cost
	}
	return out, nil
}

func (s *Store) projectCost(slug string) (float64, error) {
	rows, err := s.db.Query(`
		SELECT COALESCE(primary_model,''),
		       total_input_tokens, total_output_tokens,
		       total_cache_read_tokens, total_cache_creation_tokens
		FROM sessions WHERE project_slug = ?
	`, slug)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var total float64
	for rows.Next() {
		var model string
		var in, out, cr, cc int64
		if err := rows.Scan(&model, &in, &out, &cr, &cc); err != nil {
			return 0, err
		}
		total += pricing.CostUSD(model, in, out, cr, cc)
	}
	return total, rows.Err()
}

// ------------------------------------------------------------------
// Files (ingestion tracking)
// ------------------------------------------------------------------

type FileState struct {
	ID            int64
	ProjectSlug   string
	Path          string
	SizeBytes     int64
	MtimeNs       int64
	BytesIngested int64
	Status        string
}

// GetOrCreateFile records a JSONL file and returns its current state.
// If the on-disk size is *smaller* than what we've ingested, the file was
// rotated or truncated — we reset bytes_ingested to 0. That path also nukes
// any messages that came from that file (caller can do this).
func (s *Store) GetOrCreateFile(slug, path string, size, mtimeNs int64) (*FileState, bool, error) {
	var fs FileState
	err := s.db.QueryRow(`
		SELECT id, project_slug, path, size_bytes, mtime_ns, bytes_ingested, ingest_status
		FROM files WHERE path = ?
	`, path).Scan(&fs.ID, &fs.ProjectSlug, &fs.Path, &fs.SizeBytes, &fs.MtimeNs, &fs.BytesIngested, &fs.Status)
	if err == sql.ErrNoRows {
		now := nowISO()
		res, err := s.db.Exec(`
			INSERT INTO files(project_slug, path, size_bytes, mtime_ns, bytes_ingested,
			                  ingest_status, first_ingested_at, last_ingested_at)
			VALUES(?, ?, ?, ?, 0, 'pending', ?, ?)
		`, slug, path, size, mtimeNs, now, now)
		if err != nil {
			return nil, false, err
		}
		id, _ := res.LastInsertId()
		return &FileState{ID: id, ProjectSlug: slug, Path: path, SizeBytes: size,
			MtimeNs: mtimeNs, BytesIngested: 0, Status: "pending"}, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	// Detect truncation/rotation.
	if size < fs.BytesIngested {
		fs.BytesIngested = 0
	}
	fs.SizeBytes = size
	fs.MtimeNs = mtimeNs
	return &fs, false, nil
}

// UpdateFileProgress records progress and status.
func (s *Store) UpdateFileProgress(fileID, bytesIngested, sizeBytes, mtimeNs int64, status, lastErr string) error {
	_, err := s.db.Exec(`
		UPDATE files
		SET bytes_ingested = ?, size_bytes = ?, mtime_ns = ?,
		    ingest_status = ?, last_error = ?, last_ingested_at = ?
		WHERE id = ?
	`, bytesIngested, sizeBytes, mtimeNs, status, lastErr, nowISO(), fileID)
	return err
}

// ------------------------------------------------------------------
// Sessions
// ------------------------------------------------------------------

// UpsertSession creates or updates the session row.
func (s *Store) UpsertSession(sessionID, projectSlug string, fileID int64, cwd, gitBranch, aiTitle, startedAt string) error {
	_, err := s.db.Exec(`
		INSERT INTO sessions(session_id, file_id, project_slug, cwd, git_branch, ai_title, started_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
		  cwd        = COALESCE(NULLIF(excluded.cwd, ''), sessions.cwd),
		  git_branch = COALESCE(NULLIF(excluded.git_branch, ''), sessions.git_branch),
		  ai_title   = COALESCE(NULLIF(excluded.ai_title, ''), sessions.ai_title),
		  started_at = COALESCE(sessions.started_at, excluded.started_at)
	`, sessionID, fileID, projectSlug, cwd, gitBranch, aiTitle, startedAt)
	return err
}

// UpdateSessionEnd records the latest timestamp seen in the session.
func (s *Store) UpdateSessionEnd(sessionID, endedAt string) error {
	_, err := s.db.Exec(`UPDATE sessions SET ended_at = ? WHERE session_id = ?`, endedAt, sessionID)
	return err
}

// RecomputeSessionAggregates rebuilds the per-session counters. Cheap enough
// to call after each file's ingestion pass.
func (s *Store) RecomputeSessionAggregates(sessionID string) error {
	if _, err := s.db.Exec(`
		UPDATE sessions SET
		  message_count               = (SELECT COUNT(*) FROM messages   WHERE session_id = ?1),
		  tool_call_count             = (SELECT COUNT(*) FROM tool_calls WHERE session_id = ?1),
		  edit_count                  = (SELECT COUNT(*) FROM tool_calls WHERE session_id = ?1 AND is_modifying=1),
		  total_input_tokens          = (SELECT COALESCE(SUM(input_tokens),0)         FROM messages WHERE session_id = ?1),
		  total_output_tokens         = (SELECT COALESCE(SUM(output_tokens),0)        FROM messages WHERE session_id = ?1),
		  total_cache_read_tokens     = (SELECT COALESCE(SUM(cache_read_tokens),0)    FROM messages WHERE session_id = ?1),
		  total_cache_creation_tokens = (SELECT COALESCE(SUM(cache_creation_tokens),0)FROM messages WHERE session_id = ?1)
		WHERE session_id = ?1
	`, sessionID); err != nil {
		return err
	}
	// Dominant model for cost estimation.
	var model sql.NullString
	_ = s.db.QueryRow(`
		SELECT model FROM messages
		WHERE session_id = ? AND model IS NOT NULL AND model <> ''
		GROUP BY model ORDER BY COUNT(*) DESC LIMIT 1
	`, sessionID).Scan(&model)
	if model.Valid {
		_, _ = s.db.Exec(`UPDATE sessions SET primary_model = ? WHERE session_id = ?`, model.String, sessionID)
	}
	return nil
}

func (s *Store) ListSessions(projectSlug string) ([]Session, error) {
	rows, err := s.db.Query(sessionSelectSQL+` WHERE s.project_slug = ? ORDER BY COALESCE(s.started_at,'') DESC`, projectSlug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		ss, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ss)
	}
	return out, rows.Err()
}

const sessionSelectSQL = `
SELECT s.session_id, s.project_slug, COALESCE(s.cwd,''), COALESCE(s.git_branch,''),
       COALESCE(s.ai_title,''), COALESCE(s.started_at,''), COALESCE(s.ended_at,''),
       s.message_count, s.tool_call_count, s.edit_count,
       (SELECT COUNT(*) FROM episodes WHERE session_id = s.session_id),
       s.total_input_tokens, s.total_output_tokens,
       s.total_cache_read_tokens, s.total_cache_creation_tokens,
       COALESCE(f.path,''), COALESCE(f.size_bytes,0), COALESCE(f.bytes_ingested,0),
       COALESCE(s.primary_model,'')
FROM sessions s
LEFT JOIN files f ON f.id = s.file_id`

func scanSession(r rowScanner) (Session, error) {
	var s Session
	if err := r.Scan(&s.SessionID, &s.ProjectSlug, &s.CWD, &s.GitBranch,
		&s.AITitle, &s.StartedAt, &s.EndedAt,
		&s.MessageCount, &s.ToolCallCount, &s.EditCount, &s.EpisodeCount,
		&s.TotalInputTokens, &s.TotalOutputTokens,
		&s.TotalCacheReadTokens, &s.TotalCacheCreationTokens,
		&s.SourcePath, &s.SizeBytes, &s.BytesIngested,
		&s.PrimaryModel); err != nil {
		return s, err
	}
	s.EstimatedCostUSD = pricing.CostUSD(s.PrimaryModel,
		s.TotalInputTokens, s.TotalOutputTokens,
		s.TotalCacheReadTokens, s.TotalCacheCreationTokens)
	return s, nil
}

// ------------------------------------------------------------------
// Messages / tool calls (insert helpers used by the ingester)
// ------------------------------------------------------------------

// MessageInsert bundles the fields the ingester writes.
type MessageInsert struct {
	SessionID           string
	UUID                string
	ParentUUID          string
	Seq                 int
	Role                string
	EventType           string
	EventSubtype        string
	Ts                  string
	Model               string
	TextContent         string
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
	IsSidechain         bool
	IsMeta              bool
	FileOffset          int64
	RawJSON             string
}

func (s *Store) InsertMessage(tx *sql.Tx, m MessageInsert) (int64, error) {
	res, err := tx.Exec(`
		INSERT INTO messages(session_id, uuid, parent_uuid, seq, role, event_type, event_subtype,
		                     ts, model, text_content, input_tokens, output_tokens,
		                     cache_read_tokens, cache_creation_tokens, is_sidechain, is_meta,
		                     file_offset, raw_json)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	`, m.SessionID, nullIfEmpty(m.UUID), nullIfEmpty(m.ParentUUID), m.Seq, m.Role, m.EventType, nullIfEmpty(m.EventSubtype),
		nullIfEmpty(m.Ts), nullIfEmpty(m.Model), m.TextContent, m.InputTokens, m.OutputTokens,
		m.CacheReadTokens, m.CacheCreationTokens, boolInt(m.IsSidechain), boolInt(m.IsMeta),
		m.FileOffset, m.RawJSON)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

type ToolCallInsert struct {
	SessionID     string
	MessageID     int64
	Seq           int
	ToolUseID     string
	Name          string
	InputJSON     string
	FilePath      string
	IsModifying   bool
	IsRead        bool
	IsBash        bool
	IsSearch      bool
	IsSubagent    bool
	Ts            string
	BashProgram   string
	BashAction    string
}

func (s *Store) InsertToolCall(tx *sql.Tx, tc ToolCallInsert) (int64, error) {
	res, err := tx.Exec(`
		INSERT INTO tool_calls(session_id, message_id, seq, tool_use_id, name, input_json, file_path,
		                       is_modifying, is_read, is_bash, is_search, is_subagent, ts,
		                       bash_program, bash_action)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	`, tc.SessionID, tc.MessageID, tc.Seq, nullIfEmpty(tc.ToolUseID), tc.Name, tc.InputJSON, nullIfEmpty(tc.FilePath),
		boolInt(tc.IsModifying), boolInt(tc.IsRead), boolInt(tc.IsBash), boolInt(tc.IsSearch), boolInt(tc.IsSubagent),
		nullIfEmpty(tc.Ts), nullIfEmpty(tc.BashProgram), nullIfEmpty(tc.BashAction))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateToolResult attaches a tool_result summary to a previously-recorded tool_call.
func (s *Store) UpdateToolResult(tx *sql.Tx, toolUseID string, resultSize int64, isError bool) error {
	_, err := tx.Exec(`
		UPDATE tool_calls SET result_size = ?, result_is_error = ? WHERE tool_use_id = ?
	`, resultSize, boolInt(isError), toolUseID)
	return err
}

// NextSeq returns the next seq value for a session's messages.
func (s *Store) NextMessageSeq(sessionID string) (int, error) {
	var n sql.NullInt64
	err := s.db.QueryRow(`SELECT MAX(seq) FROM messages WHERE session_id = ?`, sessionID).Scan(&n)
	if err != nil {
		return 0, err
	}
	if n.Valid {
		return int(n.Int64) + 1, nil
	}
	return 1, nil
}

func (s *Store) NextToolCallSeq(sessionID string) (int, error) {
	var n sql.NullInt64
	err := s.db.QueryRow(`SELECT MAX(seq) FROM tool_calls WHERE session_id = ?`, sessionID).Scan(&n)
	if err != nil {
		return 0, err
	}
	if n.Valid {
		return int(n.Int64) + 1, nil
	}
	return 1, nil
}

// ------------------------------------------------------------------
// Episodes / watermarks / drill-in queries
// ------------------------------------------------------------------

func (s *Store) DeleteEpisodesForSession(sessionID string) error {
	if _, err := s.db.Exec(`UPDATE tool_calls SET episode_id = NULL WHERE session_id = ?`, sessionID); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM episodes WHERE session_id = ?`, sessionID)
	return err
}

type EpisodeInsert struct {
	SessionID           string
	Seq                 int
	UserMessageID       int64
	FirstEditToolCallID int64
	Ended               bool
	UserPromptExcerpt   string
	StartedAt           string
	FirstEditAt         string
	DurationMs          int64
	ToolCallCount       int
	FilesReadCount      int
	FilesRead           []string
	BashCount           int
	SearchCount         int
	SubagentCount       int
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
	EditFilePath        string
	ToolCallIDs         []int64

	BytesReadBeforeEdit       int64
	IntermediateArtifacts     []string
}

func (s *Store) InsertEpisode(tx *sql.Tx, e EpisodeInsert) (int64, error) {
	filesJSON, _ := json.Marshal(e.FilesRead)
	artifactsJSON, _ := json.Marshal(e.IntermediateArtifacts)
	res, err := tx.Exec(`
		INSERT INTO episodes(session_id, seq, user_message_id, first_edit_tool_call_id, ended,
		                     user_prompt_excerpt, started_at, first_edit_at, duration_ms,
		                     tool_call_count, files_read_count, files_read_json,
		                     bash_count, search_count, subagent_count,
		                     input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens,
		                     edit_file_path, bytes_read_before_edit,
		                     intermediate_artifact_count, intermediate_artifacts_json)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	`, e.SessionID, e.Seq,
		nullIfZero(e.UserMessageID), nullIfZero(e.FirstEditToolCallID), boolInt(e.Ended),
		e.UserPromptExcerpt, nullIfEmpty(e.StartedAt), nullIfEmpty(e.FirstEditAt), e.DurationMs,
		e.ToolCallCount, e.FilesReadCount, string(filesJSON),
		e.BashCount, e.SearchCount, e.SubagentCount,
		e.InputTokens, e.OutputTokens, e.CacheReadTokens, e.CacheCreationTokens,
		nullIfEmpty(e.EditFilePath), e.BytesReadBeforeEdit,
		len(e.IntermediateArtifacts), string(artifactsJSON))
	if err != nil {
		return 0, err
	}
	epID, _ := res.LastInsertId()
	if len(e.ToolCallIDs) > 0 {
		q, args := inClauseUpdate(`UPDATE tool_calls SET episode_id = ? WHERE id IN`, epID, e.ToolCallIDs)
		if _, err := tx.Exec(q, args...); err != nil {
			return 0, err
		}
	}
	return epID, nil
}

func inClauseUpdate(prefix string, setValue int64, ids []int64) (string, []interface{}) {
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]interface{}, 0, 1+len(ids))
	args = append(args, setValue)
	for _, id := range ids {
		args = append(args, id)
	}
	return prefix + " (" + placeholders + ")", args
}

func (s *Store) ListEpisodes(sessionID string) ([]Episode, error) {
	rows, err := s.db.Query(episodeSelectSQL+` WHERE session_id = ? ORDER BY seq`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	model := s.sessionModel(sessionID)
	var out []Episode
	for rows.Next() {
		e, err := scanEpisode(rows)
		if err != nil {
			return nil, err
		}
		e.CostUSD = pricing.CostUSD(model, e.InputTokens, e.OutputTokens, e.CacheReadTokens, e.CacheCreationTokens)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) sessionModel(sessionID string) string {
	var m sql.NullString
	_ = s.db.QueryRow(`SELECT primary_model FROM sessions WHERE session_id = ?`, sessionID).Scan(&m)
	if m.Valid {
		return m.String
	}
	return ""
}

const episodeSelectSQL = `
SELECT id, session_id, seq,
       COALESCE(user_message_id,0), COALESCE(first_edit_tool_call_id,0), ended,
       COALESCE(user_prompt_excerpt,''), COALESCE(started_at,''), COALESCE(first_edit_at,''),
       COALESCE(duration_ms,0), COALESCE(tool_call_count,0),
       COALESCE(files_read_count,0), COALESCE(files_read_json,'[]'),
       COALESCE(bash_count,0), COALESCE(search_count,0), COALESCE(subagent_count,0),
       COALESCE(input_tokens,0), COALESCE(output_tokens,0),
       COALESCE(cache_read_tokens,0), COALESCE(cache_creation_tokens,0),
       COALESCE(edit_file_path,''),
       COALESCE(bytes_read_before_edit,0),
       COALESCE(intermediate_artifact_count,0),
       COALESCE(intermediate_artifacts_json,'[]')
FROM episodes`

// rowScanner works for both *sql.Rows and *sql.Row.
type rowScanner interface {
	Scan(...interface{}) error
}

func scanEpisode(r rowScanner) (Episode, error) {
	var e Episode
	var ended int
	if err := r.Scan(&e.ID, &e.SessionID, &e.Seq,
		&e.UserMessageID, &e.FirstEditToolCallID, &ended,
		&e.UserPromptExcerpt, &e.StartedAt, &e.FirstEditAt,
		&e.DurationMs, &e.ToolCallCount,
		&e.FilesReadCount, &e.FilesReadJSON,
		&e.BashCount, &e.SearchCount, &e.SubagentCount,
		&e.InputTokens, &e.OutputTokens,
		&e.CacheReadTokens, &e.CacheCreationTokens,
		&e.EditFilePath,
		&e.BytesReadBeforeEdit,
		&e.IntermediateArtifactCount,
		&e.IntermediateArtifactsJSON); err != nil {
		return e, err
	}
	e.Ended = ended == 1
	return e, nil
}

func (s *Store) ListToolCallsForEpisode(episodeID int64) ([]ToolCall, error) {
	return s.queryToolCalls(`WHERE episode_id = ? ORDER BY seq`, episodeID)
}

func (s *Store) ListToolCallsForSession(sessionID string) ([]ToolCall, error) {
	return s.queryToolCalls(`WHERE session_id = ? ORDER BY seq`, sessionID)
}

func (s *Store) queryToolCalls(whereClause string, args ...interface{}) ([]ToolCall, error) {
	q := `SELECT id, session_id, message_id, seq, COALESCE(tool_use_id,''), name,
	              COALESCE(input_json,''), COALESCE(file_path,''),
	              is_modifying, is_read, is_bash, is_search, is_subagent,
	              COALESCE(ts,''), COALESCE(result_size,0), COALESCE(result_is_error,0),
	              COALESCE(episode_id,0)
	       FROM tool_calls ` + whereClause
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ToolCall
	for rows.Next() {
		var tc ToolCall
		var mod, rd, bash, srch, sub, isErr int
		if err := rows.Scan(&tc.ID, &tc.SessionID, &tc.MessageID, &tc.Seq, &tc.ToolUseID, &tc.Name,
			&tc.InputJSON, &tc.FilePath,
			&mod, &rd, &bash, &srch, &sub,
			&tc.Ts, &tc.ResultSize, &isErr, &tc.EpisodeID); err != nil {
			return nil, err
		}
		tc.IsModifying = mod == 1
		tc.IsRead = rd == 1
		tc.IsBash = bash == 1
		tc.IsSearch = srch == 1
		tc.IsSubagent = sub == 1
		tc.ResultIsError = isErr == 1
		out = append(out, tc)
	}
	return out, rows.Err()
}

func (s *Store) ListMessages(sessionID string, limit, offset int) ([]Message, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.db.Query(`
		SELECT id, session_id, COALESCE(uuid,''), COALESCE(parent_uuid,''), seq,
		       role, event_type, COALESCE(event_subtype,''), COALESCE(ts,''),
		       COALESCE(model,''), COALESCE(text_content,''),
		       COALESCE(input_tokens,0), COALESCE(output_tokens,0),
		       COALESCE(cache_read_tokens,0), COALESCE(cache_creation_tokens,0),
		       is_sidechain, is_meta
		FROM messages WHERE session_id = ?
		ORDER BY seq LIMIT ? OFFSET ?
	`, sessionID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var side, meta int
		if err := rows.Scan(&m.ID, &m.SessionID, &m.UUID, &m.ParentUUID, &m.Seq,
			&m.Role, &m.EventType, &m.EventSubtype, &m.Ts,
			&m.Model, &m.TextContent,
			&m.InputTokens, &m.OutputTokens,
			&m.CacheReadTokens, &m.CacheCreationTokens,
			&side, &meta); err != nil {
			return nil, err
		}
		m.IsSidechain = side == 1
		m.IsMeta = meta == 1
		out = append(out, m)
	}
	return out, rows.Err()
}

// ------------------------------------------------------------------
// Watermarks
// ------------------------------------------------------------------

func (s *Store) AddWatermark(sessionID, messageUUID, label, note string) (int64, error) {
	var seq sql.NullInt64
	if messageUUID != "" {
		_ = s.db.QueryRow(`SELECT seq FROM messages WHERE uuid = ?`, messageUUID).Scan(&seq)
	}
	res, err := s.db.Exec(`
		INSERT INTO watermarks(session_id, message_uuid, seq, label, note, created_at)
		VALUES(?,?,?,?,?,?)
	`, sessionID, nullIfEmpty(messageUUID), seq, label, note, nowISO())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) DeleteWatermark(id int64) error {
	_, err := s.db.Exec(`DELETE FROM watermarks WHERE id = ?`, id)
	return err
}

func (s *Store) ListWatermarks(sessionID string) ([]Watermark, error) {
	rows, err := s.db.Query(`
		SELECT id, session_id, COALESCE(message_uuid,''), COALESCE(seq,0),
		       label, COALESCE(note,''), created_at
		FROM watermarks WHERE session_id = ? ORDER BY COALESCE(seq,0), id
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Watermark
	for rows.Next() {
		var w Watermark
		if err := rows.Scan(&w.ID, &w.SessionID, &w.MessageUUID, &w.Seq,
			&w.Label, &w.Note, &w.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// ------------------------------------------------------------------
// FTS
// ------------------------------------------------------------------

type SearchHit struct {
	MessageID   int64  `json:"messageId"`
	SessionID   string `json:"sessionId"`
	ProjectSlug string `json:"projectSlug"`
	Role        string `json:"role"`
	Ts          string `json:"ts"`
	Snippet     string `json:"snippet"`
}

// Search runs an FTS query. Query strings follow SQLite FTS5 syntax.
func (s *Store) Search(query string, limit int) ([]SearchHit, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(`
		SELECT m.id, m.session_id, s.project_slug, m.role, COALESCE(m.ts,''),
		       snippet(messages_fts, 0, '<mark>', '</mark>', ' ... ', 12)
		FROM messages_fts
		JOIN messages m ON m.id = messages_fts.rowid
		JOIN sessions s ON s.session_id = m.session_id
		WHERE messages_fts MATCH ?
		ORDER BY bm25(messages_fts) LIMIT ?
	`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchHit
	for rows.Next() {
		var h SearchHit
		if err := rows.Scan(&h.MessageID, &h.SessionID, &h.ProjectSlug, &h.Role, &h.Ts, &h.Snippet); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ------------------------------------------------------------------
// Histograms & aggregates
// ------------------------------------------------------------------

// Scope identifies which slice of the DB an aggregate covers.
// One of scopeType must be set to a non-empty value; the others are ignored.
type Scope struct {
	SessionID   string
	ProjectSlug string
	Global      bool
}

func (s Scope) whereClause(alias string) (string, []interface{}) {
	if s.SessionID != "" {
		return alias + "session_id = ?", []interface{}{s.SessionID}
	}
	if s.ProjectSlug != "" {
		return alias + "session_id IN (SELECT session_id FROM sessions WHERE project_slug = ?)",
			[]interface{}{s.ProjectSlug}
	}
	return "1=1", nil
}

// ToolBreakdown returns tool-usage counts within the scope, sorted by count desc.
func (s *Store) ToolBreakdown(scope Scope) ([]ToolBreakdown, error) {
	w, args := scope.whereClause("")
	q := `SELECT name,
	              COUNT(*) AS c,
	              SUM(is_modifying) AS m,
	              SUM(COALESCE(result_is_error,0)) AS e,
	              COALESCE(SUM(result_size),0) AS bytes
	       FROM tool_calls WHERE ` + w + `
	       GROUP BY name ORDER BY c DESC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ToolBreakdown
	for rows.Next() {
		var tb ToolBreakdown
		if err := rows.Scan(&tb.Name, &tb.Count, &tb.ModifyingCount, &tb.ErrorCount, &tb.TotalResultBytes); err != nil {
			return nil, err
		}
		out = append(out, tb)
	}
	return out, rows.Err()
}

// TopFilesTouched returns the files most read/edited within the scope.
func (s *Store) TopFilesTouched(scope Scope, limit int) ([]FileTouchStat, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	w, args := scope.whereClause("")
	q := `SELECT file_path,
	              SUM(is_read)      AS reads,
	              SUM(is_modifying) AS edits,
	              COUNT(*)          AS total,
	              COUNT(DISTINCT session_id) AS sessions
	       FROM tool_calls
	       WHERE ` + w + ` AND file_path IS NOT NULL AND file_path <> ''
	         AND (is_read = 1 OR is_modifying = 1)
	       GROUP BY file_path
	       ORDER BY total DESC
	       LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FileTouchStat
	for rows.Next() {
		var f FileTouchStat
		if err := rows.Scan(&f.FilePath, &f.ReadCount, &f.EditCount, &f.TotalCount, &f.Sessions); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// EpisodeToolCallDistribution buckets episodes by tools-before-edit.
func (s *Store) EpisodeToolCallDistribution(scope Scope) ([]EpisodeBucket, error) {
	w, args := scope.whereClause("")
	q := `SELECT tool_call_count FROM episodes WHERE ` + w
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	buckets := []EpisodeBucket{
		{Label: "0", Min: 0, Max: 0},
		{Label: "1-5", Min: 1, Max: 5},
		{Label: "6-20", Min: 6, Max: 20},
		{Label: "21-50", Min: 21, Max: 50},
		{Label: "51-100", Min: 51, Max: 100},
		{Label: "100+", Min: 101, Max: 1 << 30},
	}
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		for i := range buckets {
			if n >= buckets[i].Min && n <= buckets[i].Max {
				buckets[i].Count++
				break
			}
		}
	}
	return buckets, rows.Err()
}

// BashBreakdown returns grouped (program, action) counts across a scope.
func (s *Store) BashBreakdown(scope Scope) ([]BashBreakdownRow, error) {
	w, args := scope.whereClause("")
	q := `SELECT COALESCE(bash_program,''), COALESCE(bash_action,''),
	              COUNT(*) AS c,
	              SUM(COALESCE(result_is_error,0)) AS e,
	              COUNT(DISTINCT session_id) AS s
	       FROM tool_calls
	       WHERE is_bash = 1 AND ` + w + ` AND bash_program IS NOT NULL AND bash_program <> ''
	       GROUP BY bash_program, bash_action
	       ORDER BY c DESC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BashBreakdownRow
	for rows.Next() {
		var r BashBreakdownRow
		if err := rows.Scan(&r.Program, &r.Action, &r.Count, &r.ErrorCount, &r.Sessions); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// BashSamples returns actual command executions for a (program, action) pair
// in a given scope. Pass action="" to match any action for the program.
func (s *Store) BashSamples(scope Scope, program, action string, limit int) ([]BashCommandSample, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	w, args := scope.whereClause("")
	extra := ""
	if action != "" {
		extra = " AND bash_action = ?"
		args = append([]interface{}{program, action}, args...) // shift so bash_program comes first
	} else {
		args = append([]interface{}{program}, args...)
	}
	q := `SELECT id, session_id, seq, COALESCE(ts,''), COALESCE(input_json,''),
	              COALESCE(result_size,0), COALESCE(result_is_error,0), COALESCE(episode_id,0)
	       FROM tool_calls
	       WHERE bash_program = ?` + extra + ` AND is_bash = 1 AND ` + w + `
	       ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BashCommandSample
	for rows.Next() {
		var samp BashCommandSample
		var inputJSON string
		var isErr int
		if err := rows.Scan(&samp.ID, &samp.SessionID, &samp.Seq, &samp.Ts,
			&inputJSON, &samp.ResultBytes, &isErr, &samp.EpisodeID); err != nil {
			return nil, err
		}
		samp.Command, samp.Description = extractBashCmd(inputJSON)
		samp.IsError = isErr == 1
		out = append(out, samp)
	}
	return out, rows.Err()
}

// extractBashCmd pulls `command` and `description` from a tool_use.input JSON.
func extractBashCmd(inputJSON string) (cmd, desc string) {
	if inputJSON == "" {
		return "", ""
	}
	var m struct {
		Command     string `json:"command"`
		Description string `json:"description"`
	}
	_ = json.Unmarshal([]byte(inputJSON), &m)
	return m.Command, m.Description
}

// FilesWithBlastRadius returns file-touch stats enriched with cross-project /
// cross-session counts and the average "sibling edits" per episode where the
// file was edited. High avgSiblingEdits ⇒ this file tends to cascade changes.
func (s *Store) FilesWithBlastRadius(scope Scope, limit int) ([]FileBlastRadius, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	w, args := scope.whereClause("")
	// Base per-file rollup within scope.
	q := `SELECT file_path,
	              SUM(is_read)      AS reads,
	              SUM(is_modifying) AS edits,
	              COUNT(DISTINCT session_id) AS sessions,
	              COUNT(DISTINCT COALESCE(episode_id,0)) AS episodes,
	              MIN(COALESCE(ts,'')) AS first_seen,
	              MAX(COALESCE(ts,'')) AS last_seen
	       FROM tool_calls
	       WHERE ` + w + ` AND file_path IS NOT NULL AND file_path <> ''
	         AND (is_read = 1 OR is_modifying = 1)
	       GROUP BY file_path
	       ORDER BY (SUM(is_read) + SUM(is_modifying)) DESC
	       LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type base struct {
		Path                                      string
		Reads, Edits, Sessions, Episodes          int
		FirstSeen, LastSeen                       string
	}
	var bases []base
	for rows.Next() {
		var b base
		if err := rows.Scan(&b.Path, &b.Reads, &b.Edits, &b.Sessions, &b.Episodes, &b.FirstSeen, &b.LastSeen); err != nil {
			return nil, err
		}
		bases = append(bases, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]FileBlastRadius, 0, len(bases))
	for _, b := range bases {
		fbr := FileBlastRadius{
			FilePath:  b.Path,
			ReadCount: b.Reads,
			EditCount: b.Edits,
			Sessions:  b.Sessions,
			Episodes:  b.Episodes,
			FirstSeen: b.FirstSeen,
			LastSeen:  b.LastSeen,
		}
		// Projects touching this file (may exceed scope's project count).
		_ = s.db.QueryRow(`
			SELECT COUNT(DISTINCT s.project_slug)
			FROM tool_calls t JOIN sessions s ON s.session_id = t.session_id
			WHERE t.file_path = ? AND (t.is_read = 1 OR t.is_modifying = 1)
		`, b.Path).Scan(&fbr.Projects)

		// Avg sibling edits = average # of *other* files edited in the same
		// episodes where this file was edited.
		_ = s.db.QueryRow(`
			WITH eps AS (
			  SELECT DISTINCT episode_id FROM tool_calls
			  WHERE file_path = ? AND is_modifying = 1 AND episode_id IS NOT NULL
			)
			SELECT COALESCE(AVG(sibling_count), 0)
			FROM (
			  SELECT COUNT(DISTINCT t.file_path) - 1 AS sibling_count
			  FROM tool_calls t
			  JOIN eps ON eps.episode_id = t.episode_id
			  WHERE t.is_modifying = 1 AND t.file_path IS NOT NULL AND t.file_path <> ''
			  GROUP BY t.episode_id
			)
		`, b.Path).Scan(&fbr.AvgSiblingEdits)

		out = append(out, fbr)
	}
	return out, nil
}

// GetSession fetches a single session by id (nil if not found).
func (s *Store) GetSession(sessionID string) (*Session, error) {
	rows, err := s.db.Query(sessionSelectSQL+` WHERE s.session_id = ?`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, nil
	}
	ss, err := scanSession(rows)
	if err != nil {
		return nil, err
	}
	return &ss, nil
}

// GetEpisode fetches a single episode by id.
func (s *Store) GetEpisode(id int64) (*Episode, error) {
	rows, err := s.db.Query(episodeSelectSQL+` WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, nil
	}
	e, err := scanEpisode(rows)
	if err != nil {
		return nil, err
	}
	e.CostUSD = pricing.CostUSD(s.sessionModel(e.SessionID),
		e.InputTokens, e.OutputTokens, e.CacheReadTokens, e.CacheCreationTokens)
	return &e, nil
}

// GetMessageRaw returns the original JSON for a message id.
func (s *Store) GetMessageRaw(id int64) (string, error) {
	var raw string
	err := s.db.QueryRow(`SELECT COALESCE(raw_json,'') FROM messages WHERE id = ?`, id).Scan(&raw)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return raw, err
}

// CountMessages returns the total for pagination.
func (s *Store) CountMessages(sessionID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE session_id = ?`, sessionID).Scan(&n)
	return n, err
}

// ------------------------------------------------------------------
// helpers
// ------------------------------------------------------------------

func nowISO() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func nullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
func nullIfZero(n int64) interface{} {
	if n == 0 {
		return nil
	}
	return n
}
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
