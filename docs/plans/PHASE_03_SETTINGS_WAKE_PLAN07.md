# Phase 03 — Settings Twin Cutover (PLAN 07A)

**Status:** IN REVIEW — software settings cutover implemented; exact-head acceptance pending  
**Primary owner:** Issue `#197`

## Goal

Establish one authoritative desired/reported settings path on the selected Typed Companion Capability RPC boundary (`device.settings_v1`) and remove the legacy settings transport.

## Verified implementation scope

- PostgreSQL is authoritative for desired/reported settings state.
- Desired revisions are monotonic and owner/device scoped.
- Requested state is distinct from applied/rejected device outcome.
- Applied/rejected metadata is durable.
- Device delivery uses `capability.call` / `capability.result` only.
- Legacy `config.update` / `config.report` wire types and handlers are removed from active product source.
- Settings reconciliation is session-scoped and reasserts each non-zero desired revision on a newly authenticated session. A previous durable report is not treated as proof that a restarted runtime still holds the revision.
- Firmware and Tier-1 software-device acknowledge a new settings revision only after `CompanionApp` accepts it.
- Owner Hub backend projection uses owned device IDs and authoritative settings status instead of fabricated online/RSSI/firmware telemetry.

## Evidence boundary

The software Tier-1 scenario proves settings delivery, apply acknowledgement, reconnect behavior and protocol rejection. It does **not** prove physical acoustic quality, wake-word effectiveness, physical HIL, OTA promotion, or provider quality.

## PLAN 07B / #198 is not complete in this PR

The branch carries the canonical `wake_model` settings field as groundwork, but #198 requires more than transport plumbing. Before #198 can close it still needs:

- discovery/exposure of only wake modes/models actually packaged in the exact firmware artifact;
- deterministic wake-disabled -> PTT fallback;
- safe ESP-SR model/threshold reconfiguration inside the selected Audio owner;
- previous-good or disabled fallback when model initialization fails;
- truthful applied/rejected reporting for the **actual active** wake configuration;
- deterministic speaking/listening/reconnect/reboot behavior;
- exact resource and model provenance evidence;
- physical acoustic promotion only through #17.

Do not cite the presence of `wake_model` in the settings schema or a successful capability acknowledgement as proof that the active ESP-SR WakeNet model changed.

## #228 status

This PR can satisfy the settings-related portion of #228 A4/A8 by deleting the parallel settings transport, but #228 remains open until its complete A1–A8 final review passes. This plan must not close #228 by assertion.

## Remaining acceptance before #197 merge

1. Fresh #197 semantic review against exact source.
2. Exact-head evidence/single-path gate.
3. Host component tests.
4. Go backend quality.
5. ESP32-S3 firmware compile.
6. PostgreSQL integration/recovery.
7. Tier-1 software-device orchestration using the canonical `settings_update_apply` scenario.
8. No temporary mutation workflow or legacy settings compatibility path in the final tree.

#197 may close only when those exact-head checks and the fresh semantic review pass. #198 remains open for PLAN 07B.