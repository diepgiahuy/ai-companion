#!/usr/bin/env bash
set -euo pipefail

echo '== host firmware simulation =='
cmake -S . -B build-host -G Ninja -DCMAKE_BUILD_TYPE=Release
cmake --build build-host
ctest --test-dir build-host --output-on-failure
python3 scripts/budget_check.py

echo '== backend Go 1.26.6 + race + canonical adapter compile/test gates =='
(cd backend && test "$(go env GOVERSION)" = "go1.26.6")
(cd backend && go test -tags "adk,mcp,nolibopusfile" -race -count=1 ./...)

echo 'E2E PASS (software integration only; real provider/network/HIL gates are tracked separately)'
