#!/usr/bin/env bash
set -euo pipefail

MODE="${1:-}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT/backend"

test "$(GOTOOLCHAIN=local go env GOVERSION)" = "go1.26.6" || {
  echo "backend quality requires go1.26.6; got $(GOTOOLCHAIN=local go env GOVERSION)" >&2
  exit 1
}

case "$MODE" in
  fast)
    go mod verify
    go test -tags "adk,mcp,nolibopusfile" -count=1 ./...
    ;;
  full)
    command -v govulncheck >/dev/null 2>&1 || {
      echo "govulncheck must be preinstalled for full quality mode" >&2
      exit 1
    }
    tmpdir="$(mktemp -d)"
    trap 'rm -rf "$tmpdir"' EXIT
    cp go.mod "$tmpdir/go.mod.before"
    cp go.sum "$tmpdir/go.sum.before"
    go mod tidy
    cmp go.mod "$tmpdir/go.mod.before"
    cmp go.sum "$tmpdir/go.sum.before"
    go mod verify
    go vet -tags "adk,mcp,nolibopusfile" ./...
    go test -tags "adk,mcp,nolibopusfile" -race -count=1 ./...
    govulncheck -scan symbol -tags=adk,mcp,nolibopusfile -test ./...
    ;;
  *)
    echo "usage: $0 fast|full" >&2
    exit 2
    ;;
esac
