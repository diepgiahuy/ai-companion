#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

command -v go >/dev/null 2>&1 || {
  echo "check-fast requires Go 1.26.6" >&2
  exit 1
}

test "$(GOTOOLCHAIN=local go env GOVERSION)" = "go1.26.6" || {
  echo "check-fast requires exact go1.26.6; got $(GOTOOLCHAIN=local go env GOVERSION)" >&2
  exit 1
}

bash "$ROOT/scripts/e2e.sh"
bash "$ROOT/scripts/backend_quality.sh" fast
