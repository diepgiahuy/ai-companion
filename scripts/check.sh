#!/usr/bin/env bash
set -euo pipefail

# Backward-compatible developer entrypoint. Fast checks are the default loop;
# use `make check-full` for strict pre-merge Docker + ESP-IDF validation.
exec "$(cd "$(dirname "$0")" && pwd)/check_fast.sh" "$@"
