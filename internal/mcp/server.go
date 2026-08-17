// Package mcp implements a Model Context Protocol server over stdio, exposing
// the claude_trace analyzer as tools that Claude can call.
//
// Wire format: newline-delimited JSON-RPC 2.0. One message per line on stdin
// and stdout. This is compatible with the majority of MCP clients (Claude
// Desktop, Claude Code MCP integrations, mcp-inspector) that support the
// stdio transport.
//
// Supported methods:
//   initialize         -> handshake, returns server info + capabilities
//   notifications/initialized (notification, no response)
//   tools/list         -> catalog of tools with JSON Schema input
//   tools/call         -> dispatch to a tool handler
//   ping               -> health check
//
// Any unknown method returns a JSON-RPC "method not found" error.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"claude-trace/internal/analyzer"
	"claude-trace/internal/ingest"
	"claude-trace/internal/store"
)

// Server carries dependencies + I/O handles.
type Server struct {
	Store    *store.Store
	Analyzer *analyzer.Analyzer
	Ingester *ingest.Ingester

	// I/O — defaults to os.Stdin/Stdout in Run().
	In  io.Reader
	Out io.Writer

	writeMu sync.Mutex
}

// -----------------------------------------------------------------------------
// JSON-RPC 2.0 envelope
// -----------------------------------------------------------------------------

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // may be number, string, or absent (notification)
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

const (
	errParse     = -32700
	errInvalid   = -32600
	errNotFound  = -32601
	errBadParams = -32602
	errInternal  = -32603
)

// -----------------------------------------------------------------------------
// Run loop
// -----------------------------------------------------------------------------

// Run reads newline-delimited JSON-RPC messages from In, dispatches them, and
// writes responses to Out. Blocks until In closes or ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	scanner := bufio.NewScanner(s.In)
	// Allow large messages (some tool payloads carry a lot of JSON).
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			s.sendError(nil, errParse, "parse error: "+err.Error())
			continue
		}
		s.dispatch(ctx, &req)
	}
	return scanner.Err()
}

func (s *Server) dispatch(ctx context.Context, req *rpcRequest) {
	// Notification (no id) → run but don't reply.
	isNotification := len(req.ID) == 0

	switch req.Method {
	case "initialize":
		s.reply(req.ID, s.initializeResult(), nil)
	case "initialized", "notifications/initialized":
		// no-op notification
		_ = isNotification
	case "ping":
		s.reply(req.ID, map[string]string{}, nil)
	case "tools/list":
		s.reply(req.ID, map[string]interface{}{"tools": toolDefs()}, nil)
	case "tools/call":
		result, err := s.handleToolCall(ctx, req.Params)
		if err != nil {
			// Tool errors are conveyed as content with isError=true, per MCP.
			s.reply(req.ID, map[string]interface{}{
				"content": []map[string]string{{"type": "text", "text": err.Error()}},
				"isError": true,
			}, nil)
			return
		}
		s.reply(req.ID, result, nil)
	default:
		if !isNotification {
			s.sendError(req.ID, errNotFound, "method not found: "+req.Method)
		}
	}
}

func (s *Server) reply(id json.RawMessage, result interface{}, e *rpcError) {
	if id == nil {
		return // notification — no reply
	}
	resp := rpcResponse{JSONRPC: "2.0", ID: id, Result: result, Error: e}
	s.writeMsg(resp)
}

func (s *Server) sendError(id json.RawMessage, code int, msg string) {
	if id == nil {
		id = json.RawMessage("null")
	}
	resp := rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
	s.writeMsg(resp)
}

func (s *Server) writeMsg(v interface{}) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	b = append(b, '\n')
	_, _ = s.Out.Write(b)
}

// -----------------------------------------------------------------------------
// initialize
// -----------------------------------------------------------------------------

func (s *Server) initializeResult() map[string]interface{} {
	return map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]interface{}{
			"tools": map[string]interface{}{},
		},
		"serverInfo": map[string]interface{}{
			"name":    "claude-trace",
			"version": "0.1.0",
		},
	}
}

// -----------------------------------------------------------------------------
// Tool catalog + dispatch
// -----------------------------------------------------------------------------

type toolDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

func toolDefs() []toolDef {
	obj := func(props map[string]interface{}, required ...string) map[string]interface{} {
		m := map[string]interface{}{"type": "object", "properties": props}
		if len(required) > 0 {
			m["required"] = required
		}
		return m
	}
	str := func(desc string) map[string]interface{} {
		return map[string]interface{}{"type": "string", "description": desc}
	}
	num := func(desc string) map[string]interface{} {
		return map[string]interface{}{"type": "integer", "description": desc}
	}
	scope := map[string]interface{}{
		"type":        "string",
		"enum":        []string{"session", "project", "global"},
		"description": "aggregation scope",
	}

	return []toolDef{
		{
			Name:        "list_projects",
			Description: "List all Claude Code projects that have been ingested, with per-project totals (sessions, tool calls, edits, tokens).",
			InputSchema: obj(nil),
		},
		{
			Name:        "list_sessions",
			Description: "List sessions in a project. project_slug is the folder name under ~/.claude/projects (see list_projects).",
			InputSchema: obj(map[string]interface{}{
				"project_slug": str("Slug from list_projects (e.g. -Users-chris-hitorro)"),
			}, "project_slug"),
		},
		{
			Name:        "get_session",
			Description: "Fetch a single session's summary (counts, tokens, source file).",
			InputSchema: obj(map[string]interface{}{
				"session_id": str("Session UUID (matches the JSONL filename)"),
			}, "session_id"),
		},
		{
			Name:        "list_episodes",
			Description: "List analyzer-derived episodes for a session. An episode is one user turn → the next Edit/Write. Reveals how much reading/searching/bash happened before code was written.",
			InputSchema: obj(map[string]interface{}{
				"session_id": str("Session UUID"),
			}, "session_id"),
		},
		{
			Name:        "get_episode",
			Description: "Full detail for one episode, including its tool_call sequence in order.",
			InputSchema: obj(map[string]interface{}{
				"episode_id": num("Episode id (from list_episodes)"),
			}, "episode_id"),
		},
		{
			Name:        "session_stats",
			Description: "Bucketed episode stats for a session. If watermarks exist, splits before/after each watermark; otherwise returns 'All episodes'.",
			InputSchema: obj(map[string]interface{}{
				"session_id": str("Session UUID"),
			}, "session_id"),
		},
		{
			Name:        "tool_histogram",
			Description: "Tool-usage counts within a scope. Sorted by count desc.",
			InputSchema: obj(map[string]interface{}{
				"scope": scope,
				"id":    str("Session UUID for scope='session' or project_slug for scope='project'. Omit for 'global'."),
			}, "scope"),
		},
		{
			Name:        "top_files",
			Description: "Files most read/edited in a scope. Great for finding hot files across a project.",
			InputSchema: obj(map[string]interface{}{
				"scope": scope,
				"id":    str("Session UUID or project_slug (see scope)"),
				"limit": num("Max rows (default 50, cap 500)"),
			}, "scope"),
		},
		{
			Name:        "episode_histogram",
			Description: "Distribution of tool-call counts per episode (bucketed). Shows how often Claude wrote code with little context vs. deep exploration.",
			InputSchema: obj(map[string]interface{}{
				"scope": scope,
				"id":    str("Session UUID or project_slug (see scope)"),
			}, "scope"),
		},
		{
			Name:        "search_messages",
			Description: "Full-text search across all ingested message text (SQLite FTS5 syntax). Returns snippets with <mark> highlights.",
			InputSchema: obj(map[string]interface{}{
				"query": str("FTS5 query, e.g. 'refactor AND iterator*'"),
				"limit": num("Max hits (default 100, cap 500)"),
			}, "query"),
		},
		{
			Name:        "add_watermark",
			Description: "Add a user watermark to a session (annotates a message and creates a before/after split point for stats).",
			InputSchema: obj(map[string]interface{}{
				"session_id":   str("Session UUID"),
				"message_uuid": str("Anchor message UUID (from list_episodes or the raw log). Empty allowed."),
				"label":        str("Short label"),
				"note":         str("Longer note (optional)"),
			}, "session_id", "label"),
		},
		{
			Name:        "bash_breakdown",
			Description: "Group Bash tool_calls by (program, action) within a scope. Example: shows git commit=42, docker run=18, curl=…",
			InputSchema: obj(map[string]interface{}{
				"scope": scope,
				"id":    str("Session UUID or project_slug (see scope)"),
			}, "scope"),
		},
		{
			Name:        "bash_samples",
			Description: "Get actual Bash command executions for a (program, action) pair in a scope. Drill-in for bash_breakdown.",
			InputSchema: obj(map[string]interface{}{
				"scope":   scope,
				"id":      str("Session UUID or project_slug (see scope)"),
				"program": str("bash_program value (e.g. 'git')"),
				"action":  str("bash_action value (e.g. 'commit'); omit to match any action"),
				"limit":   num("Max samples (default 100, cap 500)"),
			}, "scope", "program"),
		},
		{
			Name:        "files_blast_radius",
			Description: "Files touched (read or edited) in a scope, with 'blast radius': distinct sessions & projects that touched the file, count of episodes, and avg number of *other* files edited in the same episodes (cascade signal). Higher avgSiblingEdits = editing this file tends to require sibling changes.",
			InputSchema: obj(map[string]interface{}{
				"scope": scope,
				"id":    str("Session UUID or project_slug (see scope)"),
				"limit": num("Max rows (default 100, cap 1000)"),
			}, "scope"),
		},
		{
			Name:        "ingest",
			Description: "Run an incremental ingestion pass over ~/.claude/projects. Fast even if most files are already up-to-date.",
			InputSchema: obj(nil),
		},
	}
}

// handleToolCall parses the JSON-RPC params and dispatches to the named tool.
func (s *Server) handleToolCall(ctx context.Context, params json.RawMessage) (map[string]interface{}, error) {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return nil, fmt.Errorf("bad tool call params: %w", err)
	}

	args := map[string]interface{}{}
	if len(call.Arguments) > 0 {
		_ = json.Unmarshal(call.Arguments, &args)
	}

	switch call.Name {
	case "list_projects":
		return textResult(s.Store.ListProjects())

	case "list_sessions":
		slug, ok := argStr(args, "project_slug")
		if !ok {
			return nil, errors.New("project_slug required")
		}
		return textResult(s.Store.ListSessions(slug))

	case "get_session":
		id, ok := argStr(args, "session_id")
		if !ok {
			return nil, errors.New("session_id required")
		}
		return textResult(s.Store.GetSession(id))

	case "list_episodes":
		id, ok := argStr(args, "session_id")
		if !ok {
			return nil, errors.New("session_id required")
		}
		return textResult(s.Store.ListEpisodes(id))

	case "get_episode":
		id, ok := argInt(args, "episode_id")
		if !ok {
			return nil, errors.New("episode_id required")
		}
		ep, err := s.Store.GetEpisode(id)
		if err != nil {
			return nil, err
		}
		if ep == nil {
			return textResult(nil, errors.New("episode not found"))
		}
		tcs, err := s.Store.ListToolCallsForEpisode(id)
		return textResult(map[string]interface{}{"episode": ep, "toolCalls": tcs}, err)

	case "session_stats":
		id, ok := argStr(args, "session_id")
		if !ok {
			return nil, errors.New("session_id required")
		}
		return textResult(s.sessionEpisodeStats(id))

	case "tool_histogram":
		scope, err := scopeFromArgs(args)
		if err != nil {
			return nil, err
		}
		return textResult(s.Store.ToolBreakdown(scope))

	case "top_files":
		scope, err := scopeFromArgs(args)
		if err != nil {
			return nil, err
		}
		limit, _ := argInt(args, "limit")
		return textResult(s.Store.TopFilesTouched(scope, int(limit)))

	case "episode_histogram":
		scope, err := scopeFromArgs(args)
		if err != nil {
			return nil, err
		}
		return textResult(s.Store.EpisodeToolCallDistribution(scope))

	case "search_messages":
		q, ok := argStr(args, "query")
		if !ok {
			return nil, errors.New("query required")
		}
		limit, _ := argInt(args, "limit")
		return textResult(s.Store.Search(q, int(limit)))

	case "add_watermark":
		sid, ok := argStr(args, "session_id")
		if !ok {
			return nil, errors.New("session_id required")
		}
		label, ok := argStr(args, "label")
		if !ok {
			return nil, errors.New("label required")
		}
		uuid, _ := argStr(args, "message_uuid")
		note, _ := argStr(args, "note")
		id, err := s.Store.AddWatermark(sid, uuid, label, note)
		return textResult(map[string]int64{"id": id}, err)

	case "bash_breakdown":
		scope, err := scopeFromArgs(args)
		if err != nil {
			return nil, err
		}
		return textResult(s.Store.BashBreakdown(scope))

	case "bash_samples":
		scope, err := scopeFromArgs(args)
		if err != nil {
			return nil, err
		}
		program, ok := argStr(args, "program")
		if !ok {
			return nil, errors.New("program required")
		}
		action, _ := argStr(args, "action")
		limit, _ := argInt(args, "limit")
		return textResult(s.Store.BashSamples(scope, program, action, int(limit)))

	case "files_blast_radius":
		scope, err := scopeFromArgs(args)
		if err != nil {
			return nil, err
		}
		limit, _ := argInt(args, "limit")
		return textResult(s.Store.FilesWithBlastRadius(scope, int(limit)))

	case "ingest":
		rep, err := s.Ingester.RunAll(ctx)
		return textResult(rep, err)
	}
	return nil, fmt.Errorf("unknown tool: %s", call.Name)
}

// textResult wraps a value in the MCP content-block envelope. If err is
// non-nil it's returned as the error (dispatch will encode isError=true).
func textResult(v interface{}, errs ...error) (map[string]interface{}, error) {
	for _, e := range errs {
		if e != nil {
			return nil, e
		}
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"content": []map[string]string{{"type": "text", "text": string(b)}},
	}, nil
}

// -----------------------------------------------------------------------------
// argument helpers
// -----------------------------------------------------------------------------

func argStr(m map[string]interface{}, key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok && s != ""
}

func argInt(m map[string]interface{}, key string) (int64, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	switch x := v.(type) {
	case float64:
		return int64(x), true
	case int:
		return int64(x), true
	case int64:
		return x, true
	case string:
		var n int64
		_, err := fmt.Sscanf(x, "%d", &n)
		return n, err == nil
	}
	return 0, false
}

func scopeFromArgs(m map[string]interface{}) (store.Scope, error) {
	s, _ := argStr(m, "scope")
	id, _ := argStr(m, "id")
	switch s {
	case "session":
		if id == "" {
			return store.Scope{}, errors.New("scope=session requires id")
		}
		return store.Scope{SessionID: id}, nil
	case "project":
		if id == "" {
			return store.Scope{}, errors.New("scope=project requires id")
		}
		return store.Scope{ProjectSlug: id}, nil
	case "global", "":
		return store.Scope{Global: true}, nil
	}
	return store.Scope{}, fmt.Errorf("unknown scope %q", s)
}

// -----------------------------------------------------------------------------
// Session stats (duplicated slim version — no import cycle back to main).
// -----------------------------------------------------------------------------

func (s *Server) sessionEpisodeStats(sessionID string) ([]map[string]interface{}, error) {
	eps, err := s.Store.ListEpisodes(sessionID)
	if err != nil {
		return nil, err
	}
	wms, err := s.Store.ListWatermarks(sessionID)
	if err != nil {
		return nil, err
	}
	buckets := []map[string]interface{}{sumBucket("All episodes", eps)}
	if len(wms) == 0 {
		return buckets, nil
	}
	for _, w := range wms {
		var before, after []store.Episode
		for _, e := range eps {
			var userSeq int
			if e.UserMessageID > 0 {
				_ = s.Store.DB().QueryRow(`SELECT seq FROM messages WHERE id = ?`, e.UserMessageID).Scan(&userSeq)
			}
			if w.Seq > 0 && userSeq > 0 && userSeq < w.Seq {
				before = append(before, e)
			} else {
				after = append(after, e)
			}
		}
		buckets = append(buckets,
			sumBucket("Before: "+w.Label, before),
			sumBucket("After: "+w.Label, after),
		)
	}
	return buckets, nil
}

func sumBucket(label string, eps []store.Episode) map[string]interface{} {
	b := map[string]interface{}{"label": label, "episodeCount": len(eps)}
	if len(eps) == 0 {
		return b
	}
	var tc, fr, in, out, dur int64
	var ended, bash, search, sub int
	for _, e := range eps {
		if e.Ended {
			ended++
		}
		tc += int64(e.ToolCallCount)
		fr += int64(e.FilesReadCount)
		in += e.InputTokens
		out += e.OutputTokens
		dur += e.DurationMs
		bash += e.BashCount
		search += e.SearchCount
		sub += e.SubagentCount
	}
	n := float64(len(eps))
	b["endedCount"] = ended
	b["avgToolCallsBeforeEdit"] = float64(tc) / n
	b["avgFilesReadBeforeEdit"] = float64(fr) / n
	b["avgInputTokens"] = float64(in) / n
	b["avgOutputTokens"] = float64(out) / n
	b["avgDurationMs"] = float64(dur) / n
	b["totalBashCalls"] = bash
	b["totalSearchCalls"] = search
	b["totalSubagentCalls"] = sub
	return b
}
