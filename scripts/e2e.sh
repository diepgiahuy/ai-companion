#!/usr/bin/env bash
set -euo pipefail

echo '== host firmware simulation =='
cmake -S . -B build-host -G Ninja -DCMAKE_BUILD_TYPE=Release
cmake --build build-host
ctest --test-dir build-host --output-on-failure
python3 scripts/budget_check.py

echo '== backend Go 1.25 + race + websocket/tool-loop E2E =='
(cd backend && go env GOVERSION | grep -Eq '^go1\.25([\.]|$)')
(cd backend && go test -tags nolibopusfile -race ./...)

echo 'E2E PASS'
