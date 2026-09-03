#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
BIN="$ROOT/bin/coolify-mcp"
export PATH="/usr/local/go/bin:/usr/bin:${HOME}/sdk/go/bin:${HOME}/go/bin:${HOME}/.local/go/bin:${HOME}/.local/bin:${PATH}"
export DOTENV_PATH="${DOTENV_PATH:-$ROOT/.env}"
if [[ ! -x "$BIN" ]]; then
  mkdir -p "$ROOT/bin"
  GO_BIN=""
  if [[ -x /usr/local/go/bin/go ]]; then
    GO_BIN=/usr/local/go/bin/go
  elif command -v go >/dev/null 2>&1; then
    GO_BIN="$(command -v go)"
  fi
  if [[ -z "$GO_BIN" ]]; then
    echo "go not found in PATH; cannot build $BIN" >&2
    exit 1
  fi
  (cd "$ROOT" && CGO_ENABLED=0 "$GO_BIN" build -o "$BIN" ./cmd/coolify-mcp)
fi
exec "$BIN" "$@"
