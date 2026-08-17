// Package ingest walks Claude project directories, parses their JSONL session
// files, and streams events into the analyzer store.
//
// Ingestion is incremental: we resume from the last byte offset recorded per
// file. Truncation / rotation is detected in store.GetOrCreateFile.
package ingest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"claude-trace/internal/parser"
	"claude-trace/internal/store"
)

// DefaultRoot returns the default Claude projects directory (~/.claude/projects).
func DefaultRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects")
}

// Ingester ties the parser, store, and analyzer together.
type Ingester struct {
	Root  string
	Store *store.Store

	// Analyze rebuilds episodes for a session after new messages land.
	// Injected to avoid an import cycle with the analyzer package.
	Analyze func(sessionID string) (int, error)

	// Progress is an optional callback invoked as each file completes.
	Progress func(path string, added, total int)
}

// RunAll scans the root directory and ingests everything.
func (in *Ingester) RunAll(ctx context.Context) (*store.IngestReport, error) {
	start := time.Now()
	rep := &store.IngestReport{}

	root := in.Root
	if root == "" {
		root = DefaultRoot()
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return rep, fmt.Errorf("read %s: %w", root, err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		slug := e.Name()
		projDir := filepath.Join(root, slug)
		// Ensure project row exists so it shows up even before any file is read.
		if err := in.Store.UpsertProject(slug, ""); err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("%s: %v", slug, err))
			continue
		}
		// Enumerate .jsonl files inside.
		files, err := os.ReadDir(projDir)
		if err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("%s: %v", projDir, err))
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			select {
			case <-ctx.Done():
				return rep, ctx.Err()
			default:
			}
			path := filepath.Join(projDir, f.Name())
			rep.FilesScanned++
			added, err := in.ingestFile(ctx, slug, path, rep)
			if err != nil {
				rep.Errors = append(rep.Errors, fmt.Sprintf("%s: %v", path, err))
				continue
			}
			if added > 0 {
				rep.FilesIngested++
			}
			if in.Progress != nil {
				in.Progress(path, added, rep.MessagesAdded)
			}
		}
	}

	rep.DurationMs = time.Since(start).Milliseconds()
	return rep, nil
}

// ingestFile ingests any not-yet-processed bytes from a single file. Returns
// the number of newly-added messages.
func (in *Ingester) ingestFile(ctx context.Context, slug, path string, rep *store.IngestReport) (int, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	fs, _, err := in.Store.GetOrCreateFile(slug, path, fi.Size(), fi.ModTime().UnixNano())
	if err != nil {
		return 0, err
	}
	if fs.BytesIngested >= fi.Size() {
		return 0, nil // nothing new
	}

	p, err := parser.Open(path, fs.BytesIngested)
	if err != nil {
		return 0, err
	}
	defer p.Close()

	sessionID := "" // populated from the first event that carries one
	var (
		msgSeq, tcSeq int
		msgAdded      int
		tcAdded       int
		lastCompleteOffset = fs.BytesIngested
		lastTs        string
		seenCWD       string
		seenTitle     string
	)

	db := in.Store.DB()
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	commitAndRestart := func() error {
		if tx == nil {
			return nil
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		tx = nil
		newTx, err := db.Begin()
		if err != nil {
			return err
		}
		tx = newTx
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return msgAdded, ctx.Err()
		default:
		}
		ev, err := p.Next()
		partial := errors.Is(err, parser.ErrPartialLine)
		if err != nil && !partial {
			if errors.Is(err, io.EOF) {
				break
			}
			// Malformed line — skip, but don't advance the offset past it.
			rep.Errors = append(rep.Errors, fmt.Sprintf("%s @%d: %v", path, ev.Offset, err))
			// Still advance offset since parser already consumed the bytes.
			// Otherwise we'd loop forever on this bad line.
			lastCompleteOffset = p.Offset()
			continue
		}
		if ev == nil {
			break
		}

		// Determine session for this event; header events carry sessionId too.
		if sessionID == "" && ev.SessionID != "" {
			sessionID = ev.SessionID
			// Bootstrap session row *through the tx* so we don't need a second
			// connection while the outer tx holds SQLite's write lock.
			if err := upsertSessionTx(tx, sessionID, slug, fs.ID, ev.CWD, ev.GitBranch, "", ev.Timestamp); err != nil {
				return msgAdded, err
			}
			msgSeq, err = in.Store.NextMessageSeq(sessionID)
			if err != nil {
				return msgAdded, err
			}
			tcSeq, err = in.Store.NextToolCallSeq(sessionID)
			if err != nil {
				return msgAdded, err
			}
		}
		if sessionID == "" {
			if !partial {
				lastCompleteOffset = p.Offset()
			}
			continue
		}

		// Update project cwd / session title through the tx — but only when
		// the value actually changes, not once per event.
		if ev.CWD != "" && ev.CWD != seenCWD {
			_ = upsertProjectTx(tx, slug, ev.CWD)
			_ = upsertSessionTx(tx, sessionID, slug, fs.ID, ev.CWD, ev.GitBranch, "", "")
			seenCWD = ev.CWD
		}
		if ev.AITitle != "" && ev.AITitle != seenTitle {
			_ = upsertSessionTx(tx, sessionID, slug, fs.ID, "", "", ev.AITitle, "")
			seenTitle = ev.AITitle
		}
		if ev.Timestamp != "" {
			lastTs = ev.Timestamp
		}

		if err := in.writeEvent(tx, ev, sessionID, &msgSeq, &tcSeq, &msgAdded, &tcAdded); err != nil {
			return msgAdded, fmt.Errorf("write event @%d: %w", ev.Offset, err)
		}

		if !partial {
			lastCompleteOffset = p.Offset()
		}

		// Commit every ~5000 messages to bound tx size.
		if msgAdded > 0 && msgAdded%5000 == 0 {
			if err := commitAndRestart(); err != nil {
				return msgAdded, err
			}
		}

		if partial {
			break
		}
	}

	if tx != nil {
		if err := tx.Commit(); err != nil {
			return msgAdded, err
		}
		tx = nil
	}

	// Persist offset & aggregates.
	if err := in.Store.UpdateFileProgress(fs.ID, lastCompleteOffset, fi.Size(), fi.ModTime().UnixNano(), "done", ""); err != nil {
		return msgAdded, err
	}
	if sessionID != "" {
		if lastTs != "" {
			_ = in.Store.UpdateSessionEnd(sessionID, lastTs)
		}
		_ = in.Store.RecomputeSessionAggregates(sessionID)
		if in.Analyze != nil && msgAdded > 0 {
			n, err := in.Analyze(sessionID)
			if err != nil {
				rep.Errors = append(rep.Errors, fmt.Sprintf("analyze %s: %v", sessionID, err))
			} else {
				rep.EpisodesBuilt += n
			}
		}
	}

	rep.MessagesAdded += msgAdded
	rep.ToolCallsAdded += tcAdded
	rep.BytesRead += (lastCompleteOffset - fs.BytesIngested)
	return msgAdded, nil
}

// writeEvent inserts one Event as a message row (+ any embedded tool calls).
func (in *Ingester) writeEvent(tx *sql.Tx, ev *parser.Event, sessionID string,
	msgSeq, tcSeq, msgAdded, tcAdded *int) error {

	role, sub, text, model := "", "", "", ""
	var inTok, outTok, cacheRead, cacheCreate int64
	var toolUses []parser.ContentBlock
	var toolResults []parser.ContentBlock

	switch ev.Type {
	case "user":
		role = "user"
		if ev.Message != nil {
			blocks, isList, _ := parser.DecodeContentBlocks(ev.Message.Content)
			if !isList {
				text = parser.StringContent(ev.Message.Content)
			} else {
				var sb strings.Builder
				sawText, sawResult := false, false
				for _, b := range blocks {
					switch b.Type {
					case "text":
						sb.WriteString(b.Text)
						sb.WriteString("\n")
						sawText = true
					case "tool_result":
						toolResults = append(toolResults, b)
						// Include a fingerprint of the result in text for FTS.
						sb.WriteString(stringifyResult(b.ResultContent))
						sb.WriteString("\n")
						sawResult = true
					}
				}
				text = strings.TrimSpace(sb.String())
				// If the user message carries only tool_results (no human text),
				// mark it so the episode analyzer doesn't treat it as a turn start.
				if sawResult && !sawText {
					sub = "tool_result"
				}
			}
		}
	case "assistant":
		role = "assistant"
		if ev.Message != nil {
			model = ev.Message.Model
			if ev.Message.Usage != nil {
				inTok = int64(ev.Message.Usage.InputTokens)
				outTok = int64(ev.Message.Usage.OutputTokens)
				cacheRead = int64(ev.Message.Usage.CacheReadInputTokens)
				cacheCreate = int64(ev.Message.Usage.CacheCreationInputTokens)
			}
			blocks, isList, _ := parser.DecodeContentBlocks(ev.Message.Content)
			if isList {
				var sb strings.Builder
				for _, b := range blocks {
					switch b.Type {
					case "text":
						sb.WriteString(b.Text)
						sb.WriteString("\n")
					case "tool_use":
						toolUses = append(toolUses, b)
						sb.WriteString("[tool_use ")
						sb.WriteString(b.Name)
						sb.WriteString("]\n")
					case "thinking":
						// Deliberately excluded from FTS text.
					}
				}
				text = strings.TrimSpace(sb.String())
			}
		}
	case "system":
		role = "system"
		sub = ev.Subtype
		text = parser.StringContent(ev.Content)
	default:
		// Header/meta events: mode, permission-mode, file-history-snapshot, attachment,
		// ai-title, last-prompt, queue-operation, pr-link — keep them for the timeline.
		role = "meta"
		sub = ev.Type
	}

	mid, err := in.Store.InsertMessage(tx, store.MessageInsert{
		SessionID:           sessionID,
		UUID:                ev.UUID,
		ParentUUID:          ev.ParentUUID,
		Seq:                 *msgSeq,
		Role:                role,
		EventType:           ev.Type,
		EventSubtype:        sub,
		Ts:                  ev.Timestamp,
		Model:               model,
		TextContent:         text,
		InputTokens:         inTok,
		OutputTokens:        outTok,
		CacheReadTokens:     cacheRead,
		CacheCreationTokens: cacheCreate,
		IsSidechain:         ev.IsSidechain,
		IsMeta:              ev.IsMeta,
		FileOffset:          ev.Offset,
		RawJSON:             string(ev.Raw),
	})
	if err != nil {
		return err
	}
	*msgSeq++
	*msgAdded++

	// tool_use blocks
	for _, b := range toolUses {
		tf := ClassifyTool(b.Name, b.Input)
		var bp, ba string
		if tf.IsBash {
			bp, ba = ClassifyBashInput(b.Input)
		}
		if _, err := in.Store.InsertToolCall(tx, store.ToolCallInsert{
			SessionID:   sessionID,
			MessageID:   mid,
			Seq:         *tcSeq,
			ToolUseID:   b.ID,
			Name:        b.Name,
			InputJSON:   string(b.Input),
			FilePath:    tf.FilePath,
			IsModifying: tf.IsModifying,
			IsRead:      tf.IsRead,
			IsBash:      tf.IsBash,
			IsSearch:    tf.IsSearch,
			IsSubagent:  tf.IsSubagent,
			Ts:          ev.Timestamp,
			BashProgram: bp,
			BashAction:  ba,
		}); err != nil {
			return err
		}
		*tcSeq++
		*tcAdded++
	}
	// tool_results — annotate the earlier tool_call
	for _, b := range toolResults {
		if b.ToolUseID == "" {
			continue
		}
		size := int64(len(b.ResultContent))
		if err := in.Store.UpdateToolResult(tx, b.ToolUseID, size, b.IsError); err != nil {
			return err
		}
	}
	return nil
}

// upsertSessionTx / upsertProjectTx mirror the store helpers but run through
// an active transaction so ingestion doesn't have to grab a second connection
// (which would deadlock waiting for SQLite's write lock).
func upsertSessionTx(tx *sql.Tx, sessionID, projectSlug string, fileID int64, cwd, gitBranch, aiTitle, startedAt string) error {
	_, err := tx.Exec(`
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

func upsertProjectTx(tx *sql.Tx, slug, cwd string) error {
	name := ""
	if cwd != "" {
		name = filepath.Base(cwd)
	}
	_, err := tx.Exec(`
		INSERT INTO projects(slug, cwd, display_name, last_seen_at)
		VALUES(?, ?, ?, ?)
		ON CONFLICT(slug) DO UPDATE SET
		  cwd          = COALESCE(NULLIF(excluded.cwd, ''), projects.cwd),
		  display_name = COALESCE(NULLIF(excluded.display_name, ''), projects.display_name),
		  last_seen_at = excluded.last_seen_at
	`, slug, cwd, name, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// stringifyResult reduces a tool_result content to a plain string for FTS.
func stringifyResult(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// try string
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// try list of {type: "text", text: "..."}
	var list []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &list); err == nil {
		var sb strings.Builder
		for _, it := range list {
			if it.Type == "text" {
				sb.WriteString(it.Text)
				sb.WriteString("\n")
			}
		}
		return strings.TrimSpace(sb.String())
	}
	return string(raw)
}
