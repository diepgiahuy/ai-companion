# Phase 1: CI/CD & Dev-Time Loop Acceleration

**Status:** COMPLETE  
**Primary Scope:** [`.github/workflows/ci.yml`](file:///Users/huydiepgia/Documents/GitHub/iot-cp-sw2.2/.github/workflows/ci.yml), [`scripts/ci_scope.py`](file:///Users/huydiepgia/Documents/GitHub/iot-cp-sw2.2/scripts/ci_scope.py), [`scripts/test_ci_scope.py`](file:///Users/huydiepgia/Documents/GitHub/iot-cp-sw2.2/scripts/test_ci_scope.py)

---

## 1. Goal

Eliminate CI wait bottlenecks during active development by providing fast, deterministic PR oracles (< 2 min) while preserving broad multi-system proof strictly on exact-SHA merge / Promotion.

---

## 2. Invariants & Rules

1. **Pre-Submit (PR Gate):**
   * Run only narrowest relevant oracles based on changed paths.
   * Native Go tests (`go test -tags "adk,mcp,nolibopusfile" ./...`) with `actions/setup-go` caching.
   * Host C++ component tests ([`scripts/e2e.sh`](file:///Users/huydiepgia/Documents/GitHub/iot-cp-sw2.2/scripts/e2e.sh)) with Ninja.
   * `CodeQL Go` and heavy `Docker Buildx` image compilation are **disabled on PRs**.
2. **Post-Submit (Promotion Gate on main push):**
   * Broad `-race` backend tests, `govulncheck`, `CodeQL Go`, PostgreSQL/River integration, and Tier-1 software-device simulation run on the exact main commit.
3. **No Migration/Compatibility Overhead:**
   * Dev PRs do not carry legacy compatibility shims or backward-adapter overhead.

---

## 3. Execution Record & Evidence

* **CI Classifier Updated:** `scripts/ci_scope.py` modified so `pr-ci-control` and `pr-fail-safe` set `codeql=False` and `backend_full=False`.
* **Tests Passing:** `python3 scripts/test_ci_scope.py` -> 14/14 tests PASS (0.000s).
* **Single Path Enforced:** `python3 scripts/check_single_path.py` -> PASS (363 active files).
* **Evidence Ledger Clean:** `python3 scripts/check_evidence.py` -> PASS.
