# Phase 3: Settings & Wake Configuration Rebase (PLAN 07)

**Status:** COMPLETE  
**Primary Owners:** Issue `#197` (PLAN 07A), Issue `#198` (PLAN 07B)  
**Core Components:** [`backend/internal/controlplane/`](file:///Users/huydiepgia/Documents/GitHub/iot-cp-sw2.2/backend/internal/controlplane), [`backend/internal/pgstore/`](file:///Users/huydiepgia/Documents/GitHub/iot-cp-sw2.2/backend/internal/pgstore), [`components/companion_app/`](file:///Users/huydiepgia/Documents/GitHub/iot-cp-sw2.2/components/companion_app)

---

## 1. Goal

Rebase device settings desired/reported twin and wake configuration onto the canonical `capability.*` RPC plane, completely deleting legacy `config.update` and `config.report` transports.

---

## 2. Invariants & Architecture Boundaries

1. **Storage Authority:** PostgreSQL is the sole durable truth for desired and reported twin state.
2. **Version Monotonicity:** Monotonic integer versions; stale or out-of-order patches are rejected.
3. **No Parallel Transports:** Legacy `config.update` / `config.report` wire types are deleted, leaving only `capability.call` / `capability.advertise`.
4. **Wake Configuration:** Expose only wake models physically packaged in ESP-SR inside firmware.
5. **Two-Stage Dependency Rule:**
   * Stabilize Phase 2 (06A–06E) -> execute **#228 Pre-Gate**.
   * Execute **PLAN 07A (#197)**.
   * Execute **Final #228 A4/A8 Review** and close `#228`.
   * Execute **PLAN 07B (#198)**.

---

## 3. Slice Breakdown & Live Status

* [x] **07A (#197) Desired/Reported Settings Twin:**
  * Rebase desired/reported device state on PostgreSQL store.
  * Wire settings dispatch over `capability.call`.
  * Add reboot/reconnect reconciliation.
  * Delete `config.update` and `config.report`.
* [x] **Final #228 A4/A8 Closure:**
  * Confirm `capability.*` is the sole device capability path (A4 PASS).
  * Confirm all legacy settings code is deleted (A8 PASS).
  * Close Issue `#228`.
* [x] **07B (#198) Wake Model Configuration:**
  * Discover supported wake models from ESP-SR artifact.
  * Route wake configuration through canonical settings twin.
  * Apply dynamically in `AudioEngine` with fallback.

---

## 4. Verification Oracle

```bash
# PostgreSQL twin integration:
go test -tags "adk,mcp,nolibopusfile" ./backend/internal/pgstore/... ./backend/internal/controlplane/...

# Single path check:
python3 scripts/check_single_path.py
```
