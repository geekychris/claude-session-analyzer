package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"claude-trace/internal/analyzer"
	"claude-trace/internal/api"
	"claude-trace/internal/ingest"
	"claude-trace/internal/store"
)

// App is the Wails-bound service. All exported methods become JS callables.
type App struct {
	ctx context.Context

	mu       sync.Mutex
	st       *store.Store
	analyzer *analyzer.Analyzer
	ingester *ingest.Ingester
	dbPath   string
	apiAddr  string // set when the embedded REST server starts

	ingesting bool
}

func NewApp() *App { return &App{} }

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	if err := a.openDefault(); err != nil {
		// Report but don't crash — the UI can show an error banner.
		fmt.Fprintln(os.Stderr, "startup: open db:", err)
		return
	}
	// Start the REST API on loopback with an OS-assigned port. Any tool
	// (curl, another Claude, the browser) can hit it while the app is open.
	go a.startEmbeddedAPI()
}

func (a *App) startEmbeddedAPI() {
	// Bind to :0 so the OS picks a free port — avoids collisions with other apps.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, "embedded API listen:", err)
		return
	}
	a.mu.Lock()
	a.apiAddr = ln.Addr().String()
	srv := &api.Server{Store: a.st, Analyzer: a.analyzer, Ingester: a.ingester}
	a.mu.Unlock()

	fmt.Fprintf(os.Stderr, "claude-trace REST API on http://%s/api/v1\n", a.apiAddr)
	// http.Server.Serve accepts a listener directly; we don't need srv.ListenAndServe.
	go func() {
		_ = (&stdHTTPServer{srv: srv}).Serve(ln)
	}()
}

// tiny adapter so we don't need to expose the embedded *http.Server on the
// API type just for this one caller.
type stdHTTPServer struct{ srv *api.Server }

func (h *stdHTTPServer) Serve(ln net.Listener) error {
	handler := h.srv.Handler()
	return (&http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}).Serve(ln)
}

func (a *App) openDefault() error {
	home, _ := os.UserHomeDir()
	dbPath := filepath.Join(home, ".claude", "claude_trace.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return err
	}
	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	an := &analyzer.Analyzer{Store: st}
	in := &ingest.Ingester{
		Store:   st,
		Analyze: an.RebuildSession,
		Progress: func(path string, added, total int) {
			if a.ctx != nil {
				runtime.EventsEmit(a.ctx, "ingest:progress", map[string]interface{}{
					"path":  path,
					"added": added,
					"total": total,
				})
			}
		},
	}
	a.mu.Lock()
	a.st = st
	a.analyzer = an
	a.ingester = in
	a.dbPath = dbPath
	a.mu.Unlock()
	return nil
}

// -----------------------------------------------------------------------------
// Meta
// -----------------------------------------------------------------------------

type AppInfo struct {
	DBPath       string `json:"dbPath"`
	ProjectsRoot string `json:"projectsRoot"`
	Ingesting    bool   `json:"ingesting"`
	APIBase      string `json:"apiBase"` // http://127.0.0.1:PORT/api/v1, empty until API is up
}

func (a *App) GetAppInfo() AppInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	info := AppInfo{
		DBPath:       a.dbPath,
		ProjectsRoot: ingest.DefaultRoot(),
		Ingesting:    a.ingesting,
	}
	if a.apiAddr != "" {
		info.APIBase = "http://" + a.apiAddr + "/api/v1"
	}
	return info
}

// -----------------------------------------------------------------------------
// Ingestion
// -----------------------------------------------------------------------------

// StartIngest kicks off a full ingestion pass in the background.
// The UI listens for "ingest:progress" and "ingest:done" events.
func (a *App) StartIngest() error {
	a.mu.Lock()
	if a.ingesting {
		a.mu.Unlock()
		return errors.New("ingest already running")
	}
	if a.ingester == nil {
		a.mu.Unlock()
		return errors.New("store not open")
	}
	a.ingesting = true
	ing := a.ingester
	a.mu.Unlock()

	go func() {
		rep, err := ing.RunAll(a.ctx)
		a.mu.Lock()
		a.ingesting = false
		a.mu.Unlock()
		payload := map[string]interface{}{"report": rep}
		if err != nil {
			payload["error"] = err.Error()
		}
		if a.ctx != nil {
			runtime.EventsEmit(a.ctx, "ingest:done", payload)
		}
	}()
	return nil
}

// -----------------------------------------------------------------------------
// Query surface
// -----------------------------------------------------------------------------

func (a *App) ListProjects() ([]store.Project, error) {
	return a.st.ListProjects()
}

func (a *App) ListSessions(projectSlug string) ([]store.Session, error) {
	return a.st.ListSessions(projectSlug)
}

func (a *App) GetSession(sessionID string) (*store.Session, error) {
	return a.st.GetSession(sessionID)
}

func (a *App) ListEpisodes(sessionID string) ([]store.Episode, error) {
	return a.st.ListEpisodes(sessionID)
}

func (a *App) ListMessages(sessionID string, limit, offset int) ([]store.Message, error) {
	return a.st.ListMessages(sessionID, limit, offset)
}

func (a *App) ListToolCallsForEpisode(episodeID int64) ([]store.ToolCall, error) {
	return a.st.ListToolCallsForEpisode(episodeID)
}

func (a *App) ListToolCallsForSession(sessionID string) ([]store.ToolCall, error) {
	return a.st.ListToolCallsForSession(sessionID)
}

// -----------------------------------------------------------------------------
// Watermarks
// -----------------------------------------------------------------------------

func (a *App) ListWatermarks(sessionID string) ([]store.Watermark, error) {
	return a.st.ListWatermarks(sessionID)
}

func (a *App) AddWatermark(sessionID, messageUUID, label, note string) (int64, error) {
	return a.st.AddWatermark(sessionID, messageUUID, label, note)
}

func (a *App) DeleteWatermark(id int64) error {
	return a.st.DeleteWatermark(id)
}

// -----------------------------------------------------------------------------
// Search
// -----------------------------------------------------------------------------

func (a *App) Search(query string, limit int) ([]store.SearchHit, error) {
	return a.st.Search(query, limit)
}

// -----------------------------------------------------------------------------
// Drill-in
// -----------------------------------------------------------------------------

// GetMessageRaw returns the original JSON line for a stored message id.
func (a *App) GetMessageRaw(id int64) (string, error) {
	var raw string
	err := a.st.DB().QueryRow(`SELECT COALESCE(raw_json,'') FROM messages WHERE id = ?`, id).Scan(&raw)
	if err == sql.ErrNoRows {
		return "", errors.New("message not found")
	}
	return raw, err
}

// RebuildEpisodes forces re-analysis for a session. Handy after an ingest bug fix.
func (a *App) RebuildEpisodes(sessionID string) (int, error) {
	return a.analyzer.RebuildSession(sessionID)
}

// CountMessages returns the total messages in a session (for pagination).
func (a *App) CountMessages(sessionID string) (int, error) {
	return a.st.CountMessages(sessionID)
}

// -----------------------------------------------------------------------------
// Histograms / top files — thin wrappers over the store, so the Wails UI
// doesn't need to hit the embedded REST server for these.
// -----------------------------------------------------------------------------

func (a *App) ToolBreakdownForSession(sessionID string) ([]store.ToolBreakdown, error) {
	return a.st.ToolBreakdown(store.Scope{SessionID: sessionID})
}
func (a *App) ToolBreakdownForProject(slug string) ([]store.ToolBreakdown, error) {
	return a.st.ToolBreakdown(store.Scope{ProjectSlug: slug})
}
func (a *App) ToolBreakdownGlobal() ([]store.ToolBreakdown, error) {
	return a.st.ToolBreakdown(store.Scope{Global: true})
}

func (a *App) TopFilesForSession(sessionID string, limit int) ([]store.FileTouchStat, error) {
	return a.st.TopFilesTouched(store.Scope{SessionID: sessionID}, limit)
}
func (a *App) TopFilesForProject(slug string, limit int) ([]store.FileTouchStat, error) {
	return a.st.TopFilesTouched(store.Scope{ProjectSlug: slug}, limit)
}

func (a *App) EpisodeHistogramForSession(sessionID string) ([]store.EpisodeBucket, error) {
	return a.st.EpisodeToolCallDistribution(store.Scope{SessionID: sessionID})
}
func (a *App) EpisodeHistogramForProject(slug string) ([]store.EpisodeBucket, error) {
	return a.st.EpisodeToolCallDistribution(store.Scope{ProjectSlug: slug})
}

// -----------------------------------------------------------------------------
// Bash drill-in
// -----------------------------------------------------------------------------

func (a *App) BashBreakdownForSession(sessionID string) ([]store.BashBreakdownRow, error) {
	return a.st.BashBreakdown(store.Scope{SessionID: sessionID})
}
func (a *App) BashBreakdownForProject(slug string) ([]store.BashBreakdownRow, error) {
	return a.st.BashBreakdown(store.Scope{ProjectSlug: slug})
}
func (a *App) BashBreakdownGlobal() ([]store.BashBreakdownRow, error) {
	return a.st.BashBreakdown(store.Scope{Global: true})
}
func (a *App) BashSamplesForSession(sessionID, program, action string, limit int) ([]store.BashCommandSample, error) {
	return a.st.BashSamples(store.Scope{SessionID: sessionID}, program, action, limit)
}
func (a *App) BashSamplesForProject(slug, program, action string, limit int) ([]store.BashCommandSample, error) {
	return a.st.BashSamples(store.Scope{ProjectSlug: slug}, program, action, limit)
}

// -----------------------------------------------------------------------------
// Files / blast radius
// -----------------------------------------------------------------------------

func (a *App) FilesBlastForSession(sessionID string, limit int) ([]store.FileBlastRadius, error) {
	return a.st.FilesWithBlastRadius(store.Scope{SessionID: sessionID}, limit)
}
func (a *App) FilesBlastForProject(slug string, limit int) ([]store.FileBlastRadius, error) {
	return a.st.FilesWithBlastRadius(store.Scope{ProjectSlug: slug}, limit)
}
func (a *App) FilesBlastGlobal(limit int) ([]store.FileBlastRadius, error) {
	return a.st.FilesWithBlastRadius(store.Scope{Global: true}, limit)
}

// -----------------------------------------------------------------------------
// Episode stats bucketed by watermark
// -----------------------------------------------------------------------------

// EpisodeStatsBucket summarises episodes falling into one bucket (before/after
// a watermark, or the whole session).
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

// SessionEpisodeStats returns whole-session and (if watermarks exist) buckets
// split by each watermark.
func (a *App) SessionEpisodeStats(sessionID string) ([]EpisodeStatsBucket, error) {
	eps, err := a.st.ListEpisodes(sessionID)
	if err != nil {
		return nil, err
	}
	wms, err := a.st.ListWatermarks(sessionID)
	if err != nil {
		return nil, err
	}

	buckets := []EpisodeStatsBucket{summarize("All episodes", eps)}
	if len(wms) == 0 {
		return buckets, nil
	}

	// Bucket by seq relative to each watermark.
	for _, w := range wms {
		var before, after []store.Episode
		for _, e := range eps {
			ownerSeq := e.Seq
			// We compare episode.seq to watermark.seq (which is the message seq).
			// A watermark placed on message N belongs "before" any episode whose
			// user_message_seq >= N. Since we don't store user_message_seq here,
			// fall back to episode.seq bucketing — episodes are strictly ordered.
			_ = ownerSeq
			if w.Seq > 0 && e.UserMessageID > 0 && a.userMessageSeq(e.UserMessageID) < w.Seq {
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

func (a *App) userMessageSeq(msgID int64) int {
	var seq int
	_ = a.st.DB().QueryRow(`SELECT seq FROM messages WHERE id = ?`, msgID).Scan(&seq)
	return seq
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
