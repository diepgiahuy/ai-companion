#!/usr/bin/env bash
set -euo pipefail

echo '== host firmware simulation =='
cmake -S . -B build-host -G Ninja -DCMAKE_BUILD_TYPE=Release
cmake --build build-host
ctest --test-dir build-host --output-on-failure
python3 scripts/budget_check.py

echo '== backend Go 1.26.5 + race + all adapter compile/test gates =='
(cd backend && go env GOVERSION | grep -Eq '^go1\.26\.5$')
(cd backend && go test -tags "adk,mcp,webrtc,nolibopusfile" -race -count=1 ./...)

echo 'E2E PASS (software integration only; real provider/network/HIL gates are tracked separately)'
