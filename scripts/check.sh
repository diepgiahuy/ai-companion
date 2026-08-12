#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BUILD="$ROOT/build-host"
mkdir -p "$BUILD"

if command -v cmake >/dev/null 2>&1; then
  cmake -S "$ROOT" -B "$BUILD" -DCMAKE_BUILD_TYPE=Release
  cmake --build "$BUILD" -j
  ctest --test-dir "$BUILD" --output-on-failure
else
  CXX="${CXX:-g++}"
  FLAGS=(-std=c++20 -O2 -Wall -Wextra -Wpedantic -Werror -I"$ROOT/components/companion_app/include")
  SOURCES=("$ROOT/components/companion_app/src/app.cpp" "$ROOT/components/companion_app/src/mock_backend.cpp" "$ROOT/components/companion_app/src/wire_protocol.cpp")
	"$CXX" "${FLAGS[@]}" -DCOMPANION_SOURCE_DIR=\"$ROOT\" "${SOURCES[@]}" "$ROOT/host/tests/tests.cpp" -o "$BUILD/companion_tests"
  "$CXX" "${FLAGS[@]}" "${SOURCES[@]}" "$ROOT/host/src/sim.cpp" -o "$BUILD/companion_sim"
  "$CXX" -std=c++20 -O2 -Wall -Wextra -Wpedantic -Werror \
    "$ROOT/host/tests/opus_probe.cpp" -ldl -o "$BUILD/opus_probe"
  "$BUILD/companion_tests"
  "$BUILD/opus_probe" || [ "$?" -eq 77 ]
fi

if [ -x "$BUILD/host/companion_tests" ]; then
  size "$BUILD/host/companion_tests" "$BUILD/host/companion_sim" || true
else
  size "$BUILD/companion_tests" "$BUILD/companion_sim" || true
fi
python3 "$ROOT/scripts/budget_check.py"

if command -v go >/dev/null 2>&1; then
  GO_VERSION="$(GOTOOLCHAIN=local go env GOVERSION 2>/dev/null || true)"
  if [[ "$GO_VERSION" =~ ^go([0-9]+)\.([0-9]+) ]] &&
     (( BASH_REMATCH[1] > 1 || (BASH_REMATCH[1] == 1 && BASH_REMATCH[2] >= 26) )); then
    (cd "$ROOT/backend" && GOTOOLCHAIN=local go test -tags nolibopusfile -race ./...)
  else
    echo "Go 1.26+ required; local ${GO_VERSION:-unknown} is too old, so backend tests remain pending."
  fi
else
  echo "Go unavailable: backend tests remain pending in this environment."
fi

if command -v idf.py >/dev/null 2>&1; then
  (cd "$ROOT" && idf.py set-target esp32s3 && idf.py build && idf.py size)
else
  echo "ESP-IDF unavailable: host code verified; target compile remains pending."
fi
