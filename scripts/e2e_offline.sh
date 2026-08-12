#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

echo '== offline host firmware simulation =='
rm -rf build-host
cmake -S . -B build-host -G Ninja -DCMAKE_BUILD_TYPE=Release
cmake --build build-host
ctest --test-dir build-host --output-on-failure
python3 scripts/budget_check.py

echo '== offline backend functional E2E + race =='
command -v go >/dev/null
command -v gcc >/dev/null
libs="$(ldconfig -p 2>/dev/null || true)"
if ! grep -q 'libsqlite3\.so' <<<"$libs"; then
  echo 'missing libsqlite3 runtime' >&2
  exit 1
fi
if ! grep -q 'libopus\.so\.0' <<<"$libs"; then
  echo 'missing libopus.so.0 runtime' >&2
  exit 1
fi
(
  cd backend
  GOTOOLCHAIN=local CGO_ENABLED=1 go test -race -count=1 -modfile=go.offline.mod ./...
)

echo 'OFFLINE E2E PASS'
