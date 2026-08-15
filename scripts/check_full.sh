#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

command -v docker >/dev/null 2>&1 || {
  echo "check-full requires Docker" >&2
  exit 1
}

docker build --pull --target quality -f Dockerfile.test -t companion-check-full .
docker run --rm companion-check-full bash scripts/e2e.sh
docker run --rm companion-check-full bash scripts/backend_quality.sh full
docker build --pull -f Dockerfile.esp-idf -t companion-esp-idf-check .
