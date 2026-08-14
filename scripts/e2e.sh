#!/usr/bin/env bash
set -euo pipefail

echo '== host firmware simulation =='
cmake -S . -B build-host -G Ninja -DCMAKE_BUILD_TYPE=Release
cmake --build build-host
ctest --test-dir build-host --output-on-failure
python3 scripts/budget_check.py

echo '== backend Go 1.26.5 + race + canonical adapter compile/test gates =='
(cd backend && go env GOVERSION | grep -Eq '^go1\.26\.5$')
set +e
GO_TEST_OUTPUT="$(cd backend && go test -tags "adk,mcp,nolibopusfile" -race -count=1 ./... 2>&1)"
GO_TEST_STATUS=$?
set -e
printf '%s\n' "$GO_TEST_OUTPUT"
if [[ "$GO_TEST_STATUS" -ne 0 ]]; then
  # GitHub's logs endpoint is not always available through automation clients.
  # Keep the real command/exit semantics unchanged, but surface a bounded tail
  # as a check annotation so the exact failing package/test remains diagnosable.
  GO_TEST_TAIL="$(printf '%s\n' "$GO_TEST_OUTPUT" | tail -c 6000)"
  GO_TEST_ANNOTATION="${GO_TEST_TAIL//'%'/'%25'}"
  GO_TEST_ANNOTATION="${GO_TEST_ANNOTATION//$'\r'/'%0D'}"
  GO_TEST_ANNOTATION="${GO_TEST_ANNOTATION//$'\n'/'%0A'}"
  echo "::error title=Backend Go race tests failed::${GO_TEST_ANNOTATION}"
  exit "$GO_TEST_STATUS"
fi

echo 'E2E PASS (software integration only; real provider/network/HIL gates are tracked separately)'
