// ct is the headless entry point for claude_trace — a small multiplexer that
// shares the same internal packages as the Wails app.
//
// Subcommands:
//
//   ct ingest                             one-shot ingestion pass
//   ct projects                           list projects
//   ct sessions <project-slug>            list sessions in a project
//   ct episodes <session-id>              list episodes in a session
//   ct serve   [--addr 127.0.0.1:8787]    run the REST API
//   ct mcp                                run the MCP server (stdio)
//
// The DB defaults to ~/.claude/claude_trace.db and can be overridden via
// CT_DB=path.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"claude-trace/internal/analyzer"
	"claude-trace/internal/api"
	"claude-trace/internal/ingest"
	"claude-trace/internal/mcp"
	"claude-trace/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}
	dbPath := os.Getenv("CT_DB")
	if dbPath == "" {
		home, _ := os.UserHomeDir()
		dbPath = filepath.Join(home, ".claude", "claude_trace.db")
	}
	_ = os.MkdirAll(filepath.Dir(dbPath), 0o755)

	st, err := store.Open(dbPath)
	must(err)
	defer st.Close()

	an := &ingest.Ingester{
		Store: st,
	}
	analz := &analyzer.Analyzer{Store: st}
	an.Analyze = analz.RebuildSession

	switch os.Args[1] {
	case "ingest":
		an.Progress = func(path string, added, total int) {
			if added > 0 {
				fmt.Fprintf(os.Stderr, "  + %s (%d new, %d total)\n", filepath.Base(path), added, total)
			}
		}
		rep, err := an.RunAll(context.Background())
		must(err)
		pretty(rep)

	case "projects":
		ps, err := st.ListProjects()
		must(err)
		pretty(ps)

	case "sessions":
		if len(os.Args) < 3 {
			die("usage: ct sessions <project-slug>")
		}
		ss, err := st.ListSessions(os.Args[2])
		must(err)
		pretty(ss)

	case "episodes":
		if len(os.Args) < 3 {
			die("usage: ct episodes <session-id>")
		}
		es, err := st.ListEpisodes(os.Args[2])
		must(err)
		pretty(es)

	case "serve":
		addr := "127.0.0.1:8787"
		for i := 2; i+1 < len(os.Args); i++ {
			if os.Args[i] == "--addr" {
				addr = os.Args[i+1]
			}
		}
		srv := &api.Server{Store: st, Analyzer: analz, Ingester: an}
		fmt.Fprintf(os.Stderr, "claude-trace REST API on http://%s/api/v1\n", addr)
		must(srv.ListenAndServe(addr))

	case "backfill-bash":
		n, err := ingest.BackfillBashClassification(context.Background(), st)
		must(err)
		fmt.Fprintf(os.Stderr, "backfilled %d bash tool_calls\n", n)

	case "rebuild-all":
		// Force re-analysis of every session (useful after adding new episode
		// fields or updated pricing). Also refreshes session aggregates so the
		// primary_model column is populated for cost estimation.
		ps, err := st.ListProjects()
		must(err)
		total := 0
		for _, p := range ps {
			ss, err := st.ListSessions(p.Slug)
			must(err)
			for _, sess := range ss {
				if err := st.RecomputeSessionAggregates(sess.SessionID); err != nil {
					fmt.Fprintln(os.Stderr, "aggregates", sess.SessionID, "->", err)
				}
				n, err := analz.RebuildSession(sess.SessionID)
				if err != nil {
					fmt.Fprintln(os.Stderr, "rebuild", sess.SessionID, "->", err)
					continue
				}
				total += n
				fmt.Fprintf(os.Stderr, "  %s: %d episodes\n", sess.SessionID[:8], n)
			}
		}
		fmt.Fprintf(os.Stderr, "rebuild-all: %d episodes across %d projects\n", total, len(ps))

	case "mcp":
		srv := &mcp.Server{
			Store: st, Analyzer: analz, Ingester: an,
			In: os.Stdin, Out: os.Stdout,
		}
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()
		must(srv.Run(ctx))

	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: ct <ingest|projects|sessions|episodes|serve|mcp|rebuild-all|backfill-bash> [args]")
	os.Exit(2)
}

func die(msg string) { fmt.Fprintln(os.Stderr, msg); os.Exit(2) }

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func pretty(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
