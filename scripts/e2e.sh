#!/usr/bin/env bash
set -euo pipefail

echo '== host firmware simulation =='
cmake -S . -B build-host -G Ninja -DCMAKE_BUILD_TYPE=Release
cmake --build build-host
ctest --test-dir build-host --output-on-failure
python3 scripts/budget_check.py

echo '== pairing RSSI harness =='
python3 scripts/test_pairing_rssi_analyze.py

echo 'E2E PASS (host/software integration only; Go race, real provider/network, simulator and physical HIL gates are owned separately)'
