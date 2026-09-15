#!/usr/bin/env bash
#
# One-line install for claude-session-analyzer (aka claude-trace).
#
#   curl -fsSL https://raw.githubusercontent.com/geekychris/claude-session-analyzer/main/scripts/install.sh | bash
#
# Builds the `ct` CLI into ~/.local/bin/ct (Chris's PATH default) and, if the
# `wails` v2 tool is available, also builds claude-trace.app into
# ~/Applications/. Re-running the script updates via `git pull` and rebuilds.
#
# Requirements:
#   git, go (>=1.25).  wails is optional but required for the .app.

set -euo pipefail

REPO_URL="https://github.com/geekychris/claude-session-analyzer.git"
BIN_DIR="${CLAUDE_TRACE_BIN_DIR:-$HOME/.local/bin}"
APPS_DIR="${CLAUDE_TRACE_APPS_DIR:-$HOME/Applications}"
CACHE_ROOT="${XDG_CACHE_HOME:-$HOME/Library/Caches}"
REPO_DIR="$CACHE_ROOT/claude-session-analyzer"

log() { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
ok()  { printf '\033[1;32m ✔ \033[0m %s\n' "$*"; }
warn(){ printf '\033[1;33m ⚠ \033[0m %s\n' "$*"; }
die() { printf '\033[1;31mERR\033[0m %s\n' "$*" >&2; exit 1; }

command -v git >/dev/null || die "git is required (install with: brew install git)"
command -v go  >/dev/null || die "go is required (install with: brew install go)"

mkdir -p "$BIN_DIR"

if [[ -d "$REPO_DIR/.git" ]]; then
  log "Updating existing checkout at $REPO_DIR"
  git -C "$REPO_DIR" pull --ff-only
else
  log "Cloning $REPO_URL to $REPO_DIR"
  mkdir -p "$(dirname "$REPO_DIR")"
  git clone --depth 1 "$REPO_URL" "$REPO_DIR"
fi

cd "$REPO_DIR"

log "Building ct → $BIN_DIR/ct"
go build -o "$BIN_DIR/ct" ./cmd/ct
ok "installed $BIN_DIR/ct"

if command -v wails >/dev/null; then
  log "Building claude-trace.app (wails detected)"
  # Silence wails' interactive prompts / rich output for CI-style output.
  wails build
  if [[ -d "build/bin/claude-trace.app" ]]; then
    mkdir -p "$APPS_DIR"
    rm -rf "$APPS_DIR/claude-trace.app"
    cp -R build/bin/claude-trace.app "$APPS_DIR/"
    ok "installed $APPS_DIR/claude-trace.app"
  else
    warn "wails build finished but build/bin/claude-trace.app was not found; skipped .app install"
  fi
else
  warn "wails not on PATH — installed only the CLI"
  echo "   To also build the native app: go install github.com/wailsapp/wails/v2/cmd/wails@latest"
fi

# PATH hint.
if ! command -v ct >/dev/null; then
  echo ""
  warn "$BIN_DIR is not on your PATH; add this to your shell profile:"
  echo "   export PATH=\"$BIN_DIR:\$PATH\""
fi

echo ""
ok "Done. Try:  ct projects"
if [[ -d "$APPS_DIR/claude-trace.app" ]]; then
  echo "        or: open -a claude-trace"
fi
