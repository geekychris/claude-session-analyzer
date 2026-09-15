// Package api exposes the analyzer store over HTTP.
//
// The REST surface mirrors the Wails bindings so external tools (curl, other
// scripts, another Claude Code instance) can query without owning the DB. The
// server is stateless — it wraps a store.Store + analyzer.Analyzer + ingester.
//
// Routes (all under /api/v1):
//
//   GET  /projects
//   GET  /projects/:slug/sessions
//   GET  /sessions/:id
//   GET  /sessions/:id/episodes
//   GET  /sessions/:id/stats           (bucketed by watermark, if any)
//   GET  /sessions/:id/messages?limit=&offset=
//   GET  /sessions/:id/tool-calls
//   GET  /sessions/:id/watermarks
//   POST /sessions/:id/watermarks      {messageUuid,label,note}
//   GET  /episodes/:id
//   GET  /episodes/:id/tool-calls
//   POST /episodes/rebuild             {sessionId}
//   GET  /watermarks/:id               (not implemented — use session listing)
//   DEL  /watermarks/:id
//   GET  /search?q=&limit=
//   GET  /tools/histogram?session=|project=|global=1
//   GET  /files/top?session=|project=|global=1&limit=
//   GET  /episodes/histogram?session=|project=|global=1
//   GET  /messages/:id/raw
//   POST /ingest
//   GET  /export/episodes.csv?session=  (session id required)
//   GET  /export/tool-calls.csv?session=
//   GET  /healthz
package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/geekychris/claude-session-analyzer/internal/analyzer"
	"github.com/geekychris/claude-session-analyzer/internal/ingest"
	"github.com/geekychris/claude-session-analyzer/internal/store"
)

// Server carries the shared state for the HTTP handlers.
type Server struct {
	Store    *store.Store
	Analyzer *analyzer.Analyzer
	Ingester *ingest.Ingester

	// Set by Serve; nil until the server starts.
	http *http.Server
}

// Handler returns the mux ready to serve. Also useful when embedding into
// an existing HTTP server (e.g. the Wails app).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Register routes. We keep path parsing manual (no third-party router) to
	// stay dependency-free.
	mux.HandleFunc("/api/v1/healthz", s.handleHealth)

	mux.HandleFunc("/api/v1/projects", s.handleListProjects)
	mux.HandleFunc("/api/v1/projects/", s.handleProjectSub)

	mux.HandleFunc("/api/v1/sessions/", s.handleSessionSub)

	mux.HandleFunc("/api/v1/episodes/", s.handleEpisodeSub)
	mux.HandleFunc("/api/v1/episodes/rebuild", s.handleRebuildEpisodes)
	mux.HandleFunc("/api/v1/episodes/histogram", s.handleEpisodeHistogram)

	mux.HandleFunc("/api/v1/watermarks/", s.handleWatermarkSub)

	mux.HandleFunc("/api/v1/search", s.handleSearch)

	mux.HandleFunc("/api/v1/tools/histogram", s.handleToolHistogram)
	mux.HandleFunc("/api/v1/files/top", s.handleTopFiles)
	mux.HandleFunc("/api/v1/files/blast-radius", s.handleFilesBlast)
	mux.HandleFunc("/api/v1/bash/breakdown", s.handleBashBreakdown)
	mux.HandleFunc("/api/v1/bash/samples", s.handleBashSamples)

	mux.HandleFunc("/api/v1/messages/", s.handleMessageSub)

	mux.HandleFunc("/api/v1/ingest", s.handleIngest)

	mux.HandleFunc("/api/v1/export/episodes.csv", s.handleExportEpisodes)
	mux.HandleFunc("/api/v1/export/tool-calls.csv", s.handleExportToolCalls)

	return withCORS(withLogging(mux))
}

// ListenAndServe starts the REST server on addr (e.g. ":8787" or "127.0.0.1:8787").
// Blocks until Shutdown is called. Loopback binding is recommended.
func (s *Server) ListenAndServe(addr string) error {
	s.http = &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s.http.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.http == nil {
		return nil
	}
	return s.http.Shutdown(ctx)
}

// -----------------------------------------------------------------------------
// Middleware
// -----------------------------------------------------------------------------

func withCORS(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Loopback usage typically fine to allow local browser access.
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func withLogging(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Keep quiet unless something is off; a real service would use a logger.
		h.ServeHTTP(w, r)
	})
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

// pathSegments splits after a known prefix. Empty trailing segments are dropped.
func pathSegments(p, prefix string) []string {
	s := strings.TrimPrefix(p, prefix)
	s = strings.Trim(s, "/")
	if s == "" {
		return nil
	}
	return strings.Split(s, "/")
}

func scopeFromQuery(r *http.Request) store.Scope {
	if v := r.URL.Query().Get("session"); v != "" {
		return store.Scope{SessionID: v}
	}
	if v := r.URL.Query().Get("project"); v != "" {
		return store.Scope{ProjectSlug: v}
	}
	if r.URL.Query().Get("global") != "" {
		return store.Scope{Global: true}
	}
	return store.Scope{Global: true}
}

// -----------------------------------------------------------------------------
// Handlers
// -----------------------------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, errors.New("method not allowed"))
		return
	}
	ps, err := s.Store.ListProjects()
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, ps)
}

// /api/v1/projects/:slug/sessions
func (s *Server) handleProjectSub(w http.ResponseWriter, r *http.Request) {
	seg := pathSegments(r.URL.Path, "/api/v1/projects/")
	if len(seg) < 2 || seg[1] != "sessions" {
		writeError(w, 404, errors.New("unknown route"))
		return
	}
	slug := seg[0]
	ss, err := s.Store.ListSessions(slug)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, ss)
}

// /api/v1/sessions/:id[/{episodes|stats|messages|tool-calls|watermarks}]
func (s *Server) handleSessionSub(w http.ResponseWriter, r *http.Request) {
	seg := pathSegments(r.URL.Path, "/api/v1/sessions/")
	if len(seg) == 0 {
		writeError(w, 404, errors.New("session id required"))
		return
	}
	id := seg[0]
	sub := ""
	if len(seg) > 1 {
		sub = seg[1]
	}
	switch sub {
	case "":
		if r.Method != http.MethodGet {
			writeError(w, 405, errors.New("method not allowed"))
			return
		}
		sess, err := s.Store.GetSession(id)
		if err != nil {
			writeError(w, 500, err)
			return
		}
		if sess == nil {
			writeError(w, 404, errors.New("session not found"))
			return
		}
		writeJSON(w, 200, sess)
	case "episodes":
		eps, err := s.Store.ListEpisodes(id)
		if err != nil {
			writeError(w, 500, err)
			return
		}
		writeJSON(w, 200, eps)
	case "stats":
		buckets, err := s.sessionEpisodeStats(id)
		if err != nil {
			writeError(w, 500, err)
			return
		}
		writeJSON(w, 200, buckets)
	case "messages":
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		msgs, err := s.Store.ListMessages(id, limit, offset)
		if err != nil {
			writeError(w, 500, err)
			return
		}
		total, _ := s.Store.CountMessages(id)
		writeJSON(w, 200, map[string]interface{}{"messages": msgs, "total": total})
	case "tool-calls":
		tcs, err := s.Store.ListToolCallsForSession(id)
		if err != nil {
			writeError(w, 500, err)
			return
		}
		writeJSON(w, 200, tcs)
	case "watermarks":
		if r.Method == http.MethodPost {
			var body struct {
				MessageUUID string `json:"messageUuid"`
				Label       string `json:"label"`
				Note        string `json:"note"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeError(w, 400, err)
				return
			}
			if body.Label == "" {
				writeError(w, 400, errors.New("label required"))
				return
			}
			wmID, err := s.Store.AddWatermark(id, body.MessageUUID, body.Label, body.Note)
			if err != nil {
				writeError(w, 500, err)
				return
			}
			writeJSON(w, 201, map[string]int64{"id": wmID})
			return
		}
		wms, err := s.Store.ListWatermarks(id)
		if err != nil {
			writeError(w, 500, err)
			return
		}
		writeJSON(w, 200, wms)
	default:
		writeError(w, 404, fmt.Errorf("unknown subresource %q", sub))
	}
}

// /api/v1/episodes/{id|rebuild|histogram}
func (s *Server) handleEpisodeSub(w http.ResponseWriter, r *http.Request) {
	// The specific paths /rebuild and /histogram are registered directly and
	// won't route through here (net/http mux prefers the more specific pattern).
	seg := pathSegments(r.URL.Path, "/api/v1/episodes/")
	if len(seg) == 0 {
		writeError(w, 404, errors.New("episode id required"))
		return
	}
	id, err := strconv.ParseInt(seg[0], 10, 64)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	if len(seg) == 1 {
		ep, err := s.Store.GetEpisode(id)
		if err != nil {
			writeError(w, 500, err)
			return
		}
		if ep == nil {
			writeError(w, 404, errors.New("episode not found"))
			return
		}
		writeJSON(w, 200, ep)
		return
	}
	switch seg[1] {
	case "tool-calls":
		tcs, err := s.Store.ListToolCallsForEpisode(id)
		if err != nil {
			writeError(w, 500, err)
			return
		}
		writeJSON(w, 200, tcs)
	default:
		writeError(w, 404, fmt.Errorf("unknown subresource %q", seg[1]))
	}
}

func (s *Server) handleRebuildEpisodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, errors.New("method not allowed"))
		return
	}
	var body struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, err)
		return
	}
	if body.SessionID == "" {
		writeError(w, 400, errors.New("sessionId required"))
		return
	}
	n, err := s.Analyzer.RebuildSession(body.SessionID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]int{"episodes": n})
}

func (s *Server) handleEpisodeHistogram(w http.ResponseWriter, r *http.Request) {
	buckets, err := s.Store.EpisodeToolCallDistribution(scopeFromQuery(r))
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, buckets)
}

func (s *Server) handleWatermarkSub(w http.ResponseWriter, r *http.Request) {
	seg := pathSegments(r.URL.Path, "/api/v1/watermarks/")
	if len(seg) == 0 {
		writeError(w, 404, errors.New("watermark id required"))
		return
	}
	id, err := strconv.ParseInt(seg[0], 10, 64)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	if r.Method != http.MethodDelete {
		writeError(w, 405, errors.New("method not allowed"))
		return
	}
	if err := s.Store.DeleteWatermark(id); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"deleted": true})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeError(w, 400, errors.New("q required"))
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	hits, err := s.Store.Search(q, limit)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, hits)
}

func (s *Server) handleToolHistogram(w http.ResponseWriter, r *http.Request) {
	tb, err := s.Store.ToolBreakdown(scopeFromQuery(r))
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, tb)
}

func (s *Server) handleTopFiles(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	files, err := s.Store.TopFilesTouched(scopeFromQuery(r), limit)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, files)
}

func (s *Server) handleFilesBlast(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	files, err := s.Store.FilesWithBlastRadius(scopeFromQuery(r), limit)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, files)
}

func (s *Server) handleBashBreakdown(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.BashBreakdown(scopeFromQuery(r))
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (s *Server) handleBashSamples(w http.ResponseWriter, r *http.Request) {
	program := r.URL.Query().Get("program")
	if program == "" {
		writeError(w, 400, errors.New("program required"))
		return
	}
	action := r.URL.Query().Get("action")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := s.Store.BashSamples(scopeFromQuery(r), program, action, limit)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (s *Server) handleMessageSub(w http.ResponseWriter, r *http.Request) {
	seg := pathSegments(r.URL.Path, "/api/v1/messages/")
	if len(seg) < 2 || seg[1] != "raw" {
		writeError(w, 404, errors.New("unknown route"))
		return
	}
	id, err := strconv.ParseInt(seg[0], 10, 64)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	raw, err := s.Store.GetMessageRaw(id)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	// Return the raw JSON as-is (not double-encoded).
	w.Header().Set("Content-Type", "application/json")
	if raw == "" {
		w.Write([]byte("null"))
		return
	}
	w.Write([]byte(raw))
}

func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, errors.New("method not allowed"))
		return
	}
	// Synchronous: caller waits for completion. Fine for CLI usage. For a UI
	// that wants progress events, use the Wails EventsEmit path instead.
	rep, err := s.Ingester.RunAll(r.Context())
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, rep)
}

// -----------------------------------------------------------------------------
// CSV export
// -----------------------------------------------------------------------------

func (s *Server) handleExportEpisodes(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session")
	if sessionID == "" {
		writeError(w, 400, errors.New("session required"))
		return
	}
	eps, err := s.Store.ListEpisodes(sessionID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="episodes.csv"`)
	c := csv.NewWriter(w)
	defer c.Flush()
	_ = c.Write([]string{
		"seq", "ended", "prompt", "toolCallsBeforeEdit", "filesRead", "bash", "search", "subagent",
		"inputTokens", "outputTokens", "cacheReadTokens", "cacheCreationTokens",
		"durationMs", "editFilePath", "startedAt", "firstEditAt",
	})
	for _, e := range eps {
		_ = c.Write([]string{
			strconv.Itoa(e.Seq), boolStr(e.Ended), e.UserPromptExcerpt,
			strconv.Itoa(e.ToolCallCount), strconv.Itoa(e.FilesReadCount),
			strconv.Itoa(e.BashCount), strconv.Itoa(e.SearchCount), strconv.Itoa(e.SubagentCount),
			strconv.FormatInt(e.InputTokens, 10), strconv.FormatInt(e.OutputTokens, 10),
			strconv.FormatInt(e.CacheReadTokens, 10), strconv.FormatInt(e.CacheCreationTokens, 10),
			strconv.FormatInt(e.DurationMs, 10), e.EditFilePath,
			e.StartedAt, e.FirstEditAt,
		})
	}
}

func (s *Server) handleExportToolCalls(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session")
	if sessionID == "" {
		writeError(w, 400, errors.New("session required"))
		return
	}
	tcs, err := s.Store.ListToolCallsForSession(sessionID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="tool-calls.csv"`)
	c := csv.NewWriter(w)
	defer c.Flush()
	_ = c.Write([]string{
		"seq", "name", "filePath", "isModifying", "isRead", "isBash", "isSearch", "isSubagent",
		"resultSize", "resultIsError", "episodeId", "ts",
	})
	for _, t := range tcs {
		_ = c.Write([]string{
			strconv.Itoa(t.Seq), t.Name, t.FilePath,
			boolStr(t.IsModifying), boolStr(t.IsRead), boolStr(t.IsBash),
			boolStr(t.IsSearch), boolStr(t.IsSubagent),
			strconv.FormatInt(t.ResultSize, 10), boolStr(t.ResultIsError),
			strconv.FormatInt(t.EpisodeID, 10), t.Ts,
		})
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// -----------------------------------------------------------------------------
// Bucketed session stats — mirrors what app.go computes for the Wails UI.
// Duplicated here so the REST server can serve it without importing main.
// -----------------------------------------------------------------------------

type EpisodeStatsBucket struct {
	Label                  string  `json:"label"`
	EpisodeCount           int     `json:"episodeCount"`
	EndedCount             int     `json:"endedCount"`
	AvgToolCallsBeforeEdit float64 `json:"avgToolCallsBeforeEdit"`
	AvgFilesReadBeforeEdit float64 `json:"avgFilesReadBeforeEdit"`
	AvgInputTokens         float64 `json:"avgInputTokens"`
	AvgOutputTokens        float64 `json:"avgOutputTokens"`
	AvgDurationMs          float64 `json:"avgDurationMs"`
	TotalBashCalls         int     `json:"totalBashCalls"`
	TotalSearchCalls       int     `json:"totalSearchCalls"`
	TotalSubagentCalls     int     `json:"totalSubagentCalls"`
}

func (s *Server) sessionEpisodeStats(sessionID string) ([]EpisodeStatsBucket, error) {
	eps, err := s.Store.ListEpisodes(sessionID)
	if err != nil {
		return nil, err
	}
	wms, err := s.Store.ListWatermarks(sessionID)
	if err != nil {
		return nil, err
	}
	buckets := []EpisodeStatsBucket{summarize("All episodes", eps)}
	if len(wms) == 0 {
		return buckets, nil
	}
	// Cache seq→episode for the split (same rule as the Wails path).
	for _, w := range wms {
		var before, after []store.Episode
		for _, e := range eps {
			userSeq := 0
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
			summarize("Before: "+w.Label, before),
			summarize("After: "+w.Label, after),
		)
	}
	return buckets, nil
}

func summarize(label string, eps []store.Episode) EpisodeStatsBucket {
	b := EpisodeStatsBucket{Label: label, EpisodeCount: len(eps)}
	if len(eps) == 0 {
		return b
	}
	var tc, fr, in, out, dur int64
	for _, e := range eps {
		if e.Ended {
			b.EndedCount++
		}
		tc += int64(e.ToolCallCount)
		fr += int64(e.FilesReadCount)
		in += e.InputTokens
		out += e.OutputTokens
		dur += e.DurationMs
		b.TotalBashCalls += e.BashCount
		b.TotalSearchCalls += e.SearchCount
		b.TotalSubagentCalls += e.SubagentCount
	}
	n := float64(len(eps))
	b.AvgToolCallsBeforeEdit = float64(tc) / n
	b.AvgFilesReadBeforeEdit = float64(fr) / n
	b.AvgInputTokens = float64(in) / n
	b.AvgOutputTokens = float64(out) / n
	b.AvgDurationMs = float64(dur) / n
	return b
}
