<p align="center">
  <img src="assets/logo.png" alt="Claude Session Analyzer logo" width="180">
</p>

# claude-trace

Analyzer for Claude Code JSONL session files. Ingests every session under
`~/.claude/projects/*/*.jsonl` into SQLite, derives **episodes** (one user
turn → the next Edit/Write), and lets you drill into how much reading /
searching / sub-agent activity happened before code was written. Ships a
native Wails UI, a headless REST API, and an MCP server.

## Install

One-line install — builds the `ct` CLI, and if `wails` is on PATH, also
builds `claude-trace.app`:

```bash
curl -fsSL https://raw.githubusercontent.com/geekychris/claude-session-analyzer/main/scripts/install.sh | bash
```

Re-run any time to update (git pull + rebuild). CLI lands in
`~/.local/bin/ct`, app in `~/Applications/claude-trace.app`. Override with
`CLAUDE_TRACE_BIN_DIR` / `CLAUDE_TRACE_APPS_DIR` env vars.

CLI-only via `go install`:

```bash
go install github.com/geekychris/claude-session-analyzer/cmd/ct@latest
```

## Deep-linking into a project

The Wails app accepts `--project <slug>` to jump straight to a project view
on launch. The slug is the `~/.claude/projects/<slug>` directory name
(Claude Code replaces both `/` and `_` in the cwd with `-`).

```bash
# LaunchServices path (may not always forward flags reliably):
open -n -a claude-trace --args --project -Users-chris-code-my-repo

# Direct binary launch (always forwards flags):
"$HOME/Applications/claude-trace.app/Contents/MacOS/claude_trace" \
    --project "-Users-chris-code-my-repo" &
```

Chief (github.com/geekychris/chief) uses this to open the analyzer directly
to the project currently focused in its sidebar.

## Quick start

```bash
# One-off ingest + inspect
go install ./cmd/ct
ct ingest
ct projects
ct sessions -Users-chris-hitorro
ct episodes 7f6f2023-7d50-4d06-9433-43a3204f2543

# Native UI
wails build && open build/bin/claude-trace.app

# Headless REST (also embedded automatically when the app runs)
ct serve --addr 127.0.0.1:8787

# MCP server (stdio)
ct mcp
```

Ingestion is incremental: byte offsets per file are stored in SQLite, so
re-runs only process new bytes.

## Data model

- **projects** — one per `~/.claude/projects/<slug>` directory
- **sessions** — one per JSONL file (a Claude Code session UUID)
- **messages** — every JSONL event (user/assistant/system/meta), preserving `raw_json`
- **tool_calls** — extracted from assistant `tool_use` blocks, with `is_modifying`/`is_read`/`is_bash`/`is_search`/`is_subagent` flags + primary `file_path`
- **episodes** — segmentation of a session by user turn → first modifying tool
- **watermarks** — user-anchored annotations on a message (splits stats before/after)
- **messages_fts** — FTS5 index over message text

## Wails UI

`wails dev` for hot-reload development, `wails build` for a packaged app.

Views:

| View       | Contents                                                          |
|------------|-------------------------------------------------------------------|
| Dashboard  | All projects with per-project counters                            |
| Project    | KPIs + sessions in the project                                    |
| Session    | Tabs: episodes / tools / stats / timeline / watermarks             |
| Episode    | Full tool-call sequence, files touched, tokens, raw drill-in      |
| Search     | FTS5 hits with snippets                                           |

The `tools` tab shows a tool-usage histogram, an episode-distribution
histogram (tools before edit), and the top files touched. The `timeline`
tab paginates 300 messages per page and lets you add watermarks with
`+ mark` beside any message.

CSV export: the session breadcrumb has direct links to `episodes.csv` and
`tool-calls.csv` (served by the embedded REST API).

## REST API

The Wails app starts a loopback REST server on an OS-assigned port when it
launches (visible in the session view breadcrumb and in `GetAppInfo`). You
can also run it headless with `ct serve --addr HOST:PORT`.

Base path: `/api/v1`

| Method | Path                                             | Description                        |
|--------|--------------------------------------------------|------------------------------------|
| GET    | `/healthz`                                       | Health check                       |
| GET    | `/projects`                                      | All projects                       |
| GET    | `/projects/:slug/sessions`                       | Sessions in a project              |
| GET    | `/sessions/:id`                                  | Session summary                    |
| GET    | `/sessions/:id/episodes`                         | Episodes                           |
| GET    | `/sessions/:id/stats`                            | Bucketed episode stats             |
| GET    | `/sessions/:id/messages?limit=&offset=`          | Paginated messages                 |
| GET    | `/sessions/:id/tool-calls`                       | All tool calls in the session      |
| GET    | `/sessions/:id/watermarks`                       | List watermarks                    |
| POST   | `/sessions/:id/watermarks`                       | Add `{messageUuid,label,note}`     |
| DELETE | `/watermarks/:id`                                | Delete a watermark                 |
| GET    | `/episodes/:id`                                  | Single episode                     |
| GET    | `/episodes/:id/tool-calls`                       | Tool calls for episode             |
| POST   | `/episodes/rebuild`                              | `{sessionId}` → re-analyze         |
| GET    | `/episodes/histogram?scope=`                     | Episode bucket distribution        |
| GET    | `/tools/histogram?session=|project=|global=1`    | Tool usage counts                  |
| GET    | `/files/top?scope=&limit=`                       | Most-touched files                 |
| GET    | `/search?q=&limit=`                              | FTS5 search                        |
| GET    | `/messages/:id/raw`                              | Raw JSON of one message            |
| POST   | `/ingest`                                        | Run ingestion (blocking)           |
| GET    | `/export/episodes.csv?session=`                  | CSV export                         |
| GET    | `/export/tool-calls.csv?session=`                | CSV export                         |

Scopes: pass `session=<uuid>` OR `project=<slug>` OR `global=1`.

Examples:

```bash
curl http://127.0.0.1:8787/api/v1/projects
curl 'http://127.0.0.1:8787/api/v1/tools/histogram?project=-Users-chris-hitorro'
curl 'http://127.0.0.1:8787/api/v1/search?q=iterator+AND+refactor&limit=20'
```

## MCP server

Speaks JSON-RPC 2.0 over stdio, newline-delimited. Tools exposed:

`list_projects`, `list_sessions`, `get_session`, `list_episodes`,
`get_episode`, `session_stats`, `tool_histogram`, `top_files`,
`episode_histogram`, `search_messages`, `add_watermark`, `ingest`.

### Register with Claude Code

Claude Code MCP server config lives in `~/.claude/settings.json` (or a
per-project `.claude/settings.local.json`) under `mcpServers`. Add:

```json
{
  "mcpServers": {
    "claude-trace": {
      "command": "/absolute/path/to/ct",
      "args": ["mcp"]
    }
  }
}
```

Or if you want to keep the DB somewhere non-default:

```json
{
  "mcpServers": {
    "claude-trace": {
      "command": "/absolute/path/to/ct",
      "args": ["mcp"],
      "env": { "CT_DB": "/absolute/path/to/claude_trace.db" }
    }
  }
}
```

Restart Claude Code. Its MCP client will discover the server and its 12
tools automatically. From then on you can ask Claude things like:

> "Use claude-trace to show me the tool histogram for project
> `-Users-chris-hitorro`, then find episodes that ran more than 30 tool
> calls before editing."

### Test with mcp-inspector or by hand

```bash
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
  '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_projects","arguments":{}}}' \
  | ct mcp
```

## Cost, code-bytes, and intermediate artifacts

Each episode records three metrics beyond tools-before-edit:

- **`bytesReadBeforeEdit`** — sum of `result_size` across Read/Grep/Glob
  calls in the episode. Answers "how much source code did Claude consume
  before writing?"
- **`intermediateArtifactCount` / `intermediateArtifactsJson`** — Writes to
  `/tmp`, `/private/tmp`, `/var/tmp`, `/var/folders`. Detects when Claude
  built a throwaway helper script (custom python, bash one-off, etc.) as
  part of resolving your prompt.
- **`costUsd`** — estimated USD spend for the episode, using the session's
  dominant model + `internal/pricing`. Session and project cards sum this.

Prices are approximate list rates and can be overridden by writing a JSON
file with the same shape and pointing `CT_PRICING=/path/to/pricing.json`.

## Prompts panel

Every UI view (dashboard, project, session, episode) has an "Ask Claude
about this" panel with copy-paste prompt cards. Each prompt is pre-filled
with the current IDs and calls the `claude-trace` MCP tools by name — so
Claude, once it has the MCP server registered, can execute the analysis
directly. Categories include:

- Cost and activity roll-ups (dashboard / project)
- Exploration-vs-code ratio (project / session)
- Cost-per-prompt breakdowns (session)
- Intermediate helper detection (session)
- Per-episode play-by-play + efficiency review (episode)

## Bash drill-in

Every Bash `tool_use` is classified into `bash_program` (git, npm, docker,
python, curl, …) plus optional `bash_action` (commit, install, run, …) at
ingest time. The classifier walks past shell comments, env-var prefixes,
sudo/env wrappers, and pipelines to find the real command. Session view
adds a **Bash** tab: click any row to see the full commands with results.

REST: `/api/v1/bash/breakdown?scope=&id=` and
`/api/v1/bash/samples?scope=&id=&program=&action=&limit=`.
MCP tools: `bash_breakdown`, `bash_samples`.

## File blast radius

Session view adds a **Files** tab with every file the session touched
(full path), sortable by:

- **Reads / Edits** (in this session)
- **Episodes** — how many episodes edited or read the file
- **Sessions\*** — how many *other* sessions across all ingested history
  touched this same file
- **Projects\*** — how many *other* projects touched it
- **Avg siblings** — average number of other files edited in the same
  episodes as this one. High values mean edits cascade: touching this file
  usually forces edits elsewhere too.

REST: `/api/v1/files/blast-radius?scope=&id=&limit=`.
MCP tool: `files_blast_radius`.

`\*` counts include the whole DB, not just the current scope — that's the
point: they reveal whether a file is central across your work.

## `ct rebuild-all`

After adding new episode metrics (or updating pricing) you can force the
analyzer to repopulate every session's episodes without re-ingesting the
JSONL:

```bash
ct rebuild-all
```

## Layout

```
claude_trace/
├── main.go, app.go              Wails bindings + embedded REST
├── cmd/ct/                      CLI (ingest / serve / mcp / …)
├── internal/
│   ├── parser/                  Streaming JSONL decoder
│   ├── store/                   SQLite schema + queries + FTS5
│   ├── analyzer/                Episode segmentation
│   ├── ingest/                  Walk + parse + write
│   ├── api/                     REST server
│   └── mcp/                     MCP server (stdio JSON-RPC)
└── frontend/                    React + Vite + TS (Wails)
```

Default DB: `~/.claude/claude_trace.db`. Override with `CT_DB=/path/db`.
