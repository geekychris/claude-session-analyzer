package store

// schemaSQL is the full DDL for the analyzer database.
//
// It's applied idempotently on every open (CREATE ... IF NOT EXISTS).
// Additive changes go here; destructive migrations would need a version table.
const schemaSQL = `
PRAGMA journal_mode = WAL;
PRAGMA synchronous  = NORMAL;
PRAGMA foreign_keys = ON;
PRAGMA temp_store   = MEMORY;

-- Projects (a project = one ~/.claude/projects/<slug> directory).
CREATE TABLE IF NOT EXISTS projects (
  slug         TEXT PRIMARY KEY,
  cwd          TEXT,
  display_name TEXT,
  last_seen_at TEXT
);

-- Source JSONL files we've ingested, with resume state.
CREATE TABLE IF NOT EXISTS files (
  id                INTEGER PRIMARY KEY,
  project_slug      TEXT NOT NULL,
  path              TEXT NOT NULL UNIQUE,
  size_bytes        INTEGER NOT NULL,
  mtime_ns          INTEGER NOT NULL,
  bytes_ingested    INTEGER NOT NULL DEFAULT 0,
  ingest_status     TEXT NOT NULL DEFAULT 'pending',
  last_error        TEXT,
  first_ingested_at TEXT NOT NULL,
  last_ingested_at  TEXT NOT NULL,
  FOREIGN KEY(project_slug) REFERENCES projects(slug)
);
CREATE INDEX IF NOT EXISTS idx_files_project ON files(project_slug);

-- Sessions (one per sessionId; usually one per file).
CREATE TABLE IF NOT EXISTS sessions (
  session_id                  TEXT PRIMARY KEY,
  file_id                     INTEGER NOT NULL,
  project_slug                TEXT NOT NULL,
  cwd                         TEXT,
  git_branch                  TEXT,
  ai_title                    TEXT,
  started_at                  TEXT,
  ended_at                    TEXT,
  message_count               INTEGER DEFAULT 0,
  tool_call_count             INTEGER DEFAULT 0,
  edit_count                  INTEGER DEFAULT 0,
  total_input_tokens          INTEGER DEFAULT 0,
  total_output_tokens         INTEGER DEFAULT 0,
  total_cache_read_tokens     INTEGER DEFAULT 0,
  total_cache_creation_tokens INTEGER DEFAULT 0,
  FOREIGN KEY(file_id)      REFERENCES files(id),
  FOREIGN KEY(project_slug) REFERENCES projects(slug)
);
CREATE INDEX IF NOT EXISTS idx_sessions_project ON sessions(project_slug);

-- Every event we care about becomes a row here (headers become a row too so we
-- preserve order & timestamps for the timeline; role='meta' for those).
CREATE TABLE IF NOT EXISTS messages (
  id                     INTEGER PRIMARY KEY,
  session_id             TEXT NOT NULL,
  uuid                   TEXT,
  parent_uuid            TEXT,
  seq                    INTEGER NOT NULL,
  role                   TEXT NOT NULL,
  event_type             TEXT NOT NULL,
  event_subtype          TEXT,
  ts                     TEXT,
  model                  TEXT,
  text_content           TEXT,
  input_tokens           INTEGER,
  output_tokens          INTEGER,
  cache_read_tokens      INTEGER,
  cache_creation_tokens  INTEGER,
  is_sidechain           INTEGER DEFAULT 0,
  is_meta                INTEGER DEFAULT 0,
  file_offset            INTEGER,
  raw_json               TEXT,
  FOREIGN KEY(session_id) REFERENCES sessions(session_id)
);
CREATE INDEX IF NOT EXISTS idx_messages_session_seq ON messages(session_id, seq);
CREATE INDEX IF NOT EXISTS idx_messages_uuid        ON messages(uuid);

-- Tool calls extracted from assistant tool_use content blocks.
CREATE TABLE IF NOT EXISTS tool_calls (
  id                INTEGER PRIMARY KEY,
  session_id        TEXT NOT NULL,
  message_id        INTEGER NOT NULL,
  seq               INTEGER NOT NULL,
  tool_use_id       TEXT,
  name              TEXT NOT NULL,
  input_json        TEXT,
  file_path         TEXT,
  is_modifying      INTEGER DEFAULT 0,
  is_read           INTEGER DEFAULT 0,
  is_bash           INTEGER DEFAULT 0,
  is_search         INTEGER DEFAULT 0,
  is_subagent       INTEGER DEFAULT 0,
  ts                TEXT,
  result_size       INTEGER,
  result_is_error   INTEGER,
  episode_id        INTEGER,
  FOREIGN KEY(session_id) REFERENCES sessions(session_id),
  FOREIGN KEY(message_id) REFERENCES messages(id)
);
CREATE INDEX IF NOT EXISTS idx_toolcalls_session_seq ON tool_calls(session_id, seq);
CREATE INDEX IF NOT EXISTS idx_toolcalls_episode    ON tool_calls(episode_id);
CREATE INDEX IF NOT EXISTS idx_toolcalls_tuid       ON tool_calls(tool_use_id);
CREATE INDEX IF NOT EXISTS idx_toolcalls_name       ON tool_calls(name);

-- Episodes: user turn → first Edit/Write in the resulting assistant chain.
CREATE TABLE IF NOT EXISTS episodes (
  id                       INTEGER PRIMARY KEY,
  session_id               TEXT NOT NULL,
  seq                      INTEGER NOT NULL,
  user_message_id          INTEGER,
  first_edit_tool_call_id  INTEGER,
  ended                    INTEGER NOT NULL,
  user_prompt_excerpt      TEXT,
  started_at               TEXT,
  first_edit_at            TEXT,
  duration_ms              INTEGER,
  tool_call_count          INTEGER,
  files_read_count         INTEGER,
  files_read_json          TEXT,
  bash_count               INTEGER,
  search_count             INTEGER,
  subagent_count           INTEGER,
  input_tokens             INTEGER,
  output_tokens            INTEGER,
  cache_read_tokens        INTEGER,
  cache_creation_tokens    INTEGER,
  edit_file_path           TEXT,
  FOREIGN KEY(session_id) REFERENCES sessions(session_id)
);
CREATE INDEX IF NOT EXISTS idx_episodes_session_seq ON episodes(session_id, seq);

-- User-defined watermarks (annotations anchored to a message).
CREATE TABLE IF NOT EXISTS watermarks (
  id           INTEGER PRIMARY KEY,
  session_id   TEXT NOT NULL,
  message_uuid TEXT,
  seq          INTEGER,
  label        TEXT NOT NULL,
  note         TEXT,
  created_at   TEXT NOT NULL,
  FOREIGN KEY(session_id) REFERENCES sessions(session_id)
);
CREATE INDEX IF NOT EXISTS idx_watermarks_session ON watermarks(session_id);

-- Full-text search on message text (populated by triggers below).
CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
  text_content,
  content='messages',
  content_rowid='id',
  tokenize='porter unicode61'
);

CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
  INSERT INTO messages_fts(rowid, text_content) VALUES (new.id, new.text_content);
END;
CREATE TRIGGER IF NOT EXISTS messages_ad AFTER DELETE ON messages BEGIN
  INSERT INTO messages_fts(messages_fts, rowid, text_content)
    VALUES ('delete', old.id, old.text_content);
END;
CREATE TRIGGER IF NOT EXISTS messages_au AFTER UPDATE ON messages BEGIN
  INSERT INTO messages_fts(messages_fts, rowid, text_content)
    VALUES ('delete', old.id, old.text_content);
  INSERT INTO messages_fts(rowid, text_content)
    VALUES (new.id, new.text_content);
END;
`
