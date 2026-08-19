# AI Companion — Production v1

AI Companion is a single-owner ESP32-S3 voice companion backed by a modular Go service. The product combines realtime Vietnamese/English interaction, durable personal tools, typed device presentation, controlled integrations, and explicit evidence boundaries between software tests, real providers, and physical hardware.

> **Immutable historical checkpoint:** [`CP-SW2.3-20260812`](https://github.com/diepgiahuy/ai-companion/tree/CP-SW2.3-20260812)
>
> **Truth model:** `main` code/schema/config is merged product truth. GitHub Issues define remaining requirements; PR descriptions explain changes; GitHub Checks/artifacts prove hosted automation; [`evidence/status.json`](evidence/status.json) records promoted evidence claims; README/architecture/ADRs document durable merged behavior and decisions.

Live branch, open-PR, and execution-queue state intentionally lives in GitHub rather than this document.

## Product shape

```text
ESP32-S3
  ├─ microphone / speaker / input / display
  ├─ ESP-SR audio front-end software path
  ├─ Opus
  └─ typed presentation + bounded device capabilities
        │
        ▼
secure WebSocket / Protocol v2
        │
        ▼
Go realtime runtime
  ├─ authenticated device session
  ├─ generation-scoped cancellation / barge-in
  ├─ streaming ASR/TTS + realtime provider boundaries
  └─ Google ADK — sole product agent runtime
        │
        ▼
ToolRegistry + policy
  ├─ native/domain tools
  ├─ server-owned device tool contracts
  └─ optional backend-side external MCP
        │
        ▼
PostgreSQL / pgx + Atlas
  ├─ authoritative domain/conversation/memory/control/auth state
  ├─ transactional outbox
  └─ River durable jobs
```

The product is intentionally a **modular monolith + firmware**, not a microservice showcase. The LLM reasons and composes actions; it is not the authoritative database.

## Durable merged capabilities

### Device and realtime runtime

- Companion Protocol v2 is the canonical device wire contract over secure WebSocket, with typed control messages and binary Opus media.
- Device sessions use unique revocable database-enrolled credentials; client-controlled identity headers cannot override enrolled ownership claims.
- Session/generation lifecycle, cancellation, stale-output suppression, bounded queues and barge-in orchestration are implemented.
- The firmware has a portable application boundary plus ESP-SR AFE/WakeNet/VAD/AEC software integration. Physical acoustic quality is a separate evidence gate.

### Agent, tools and integrations

- Google ADK is the sole product agent runtime.
- `ToolRegistry` is the server-owned model-tool definition, argument-validation, authorization, observability and execution boundary.
- Durable native tools cover expenses, budgets, notes, journal, reminders/timers, voice memos, memory, conversation and related platform behavior.
- External MCP is backend-side only. The official MCP Go SDK path and policy boundary are implemented; firmware does not run MCP.
- Device-local commands use **Typed Companion Capability RPC** over Protocol v2, not MCP-on-device. The device advertises only supported capability identity; the backend remains authoritative for accepted contracts, model visibility, schema and policy.
- Device model-tool exposure is invocation-scoped through ADK `DeviceToolset`; device-pack tools are not exported as process-wide static ADK tools.
- Product-v1 capability descriptors are command-only. A read capability requires a future explicit contract/architecture change.
- The durable capability architecture is owned by [`docs/ADR-003-DEVICE-CAPABILITY-PLANE.md`](docs/ADR-003-DEVICE-CAPABILITY-PLANE.md). Do not infer a second device protocol or device-owned model authority from reference projects.

### Data and jobs

- PostgreSQL/pgx is the **sole authoritative product store**; Atlas owns versioned schema migration.
- There is no product SQLite/PostgreSQL selector, shadow read, dual write or SQLite fallback. SQLite remains only in explicit migration/recovery tooling and isolated tests.
- Actor-scoped durable idempotency rejects same-key/different-payload conflicts and replays committed equivalent mutations across reconnect/restart.
- Transactional outbox semantics are used when durable state and event delivery must be atomic.
- River provides durable retention/background jobs after the PostgreSQL hard cut.

### Provisioning and owner recovery

- An unprovisioned Companion uses the local WPA2 setup portal to configure Wi-Fi and the backend URL.
- Upon Wi-Fi connection, the device automatically creates a device claim session (`POST /v1/device-claim-sessions`), receives a short user verification code, and displays the verification QR code URL (`/v1/owner/device-claim?s=...&user_code=...`) along with the code (`CODE AB12-CD`) on the OLED display.
- The owner scans the QR code or opens the verification link, authenticates via OIDC, and confirms device pairing with zero manual typing required.
- The device polls (`POST /v1/device-claim-sessions/token`) until approved, receives a short-lived `claim_authorization` token, and completes the credential issuance transaction at `/v1/owner/device-claims` to obtain its long-lived runtime credential.
- Device secrets (`device_code`, `user_code`) in firmware NVS are securely erased upon successful credential commit. The browser never receives or stores the long-lived device credential, and PostgreSQL never stores the human code in plaintext.
- Local factory reset does not transfer backend ownership. The same authenticated owner may atomically rotate the lost/revoked device credential; claiming by a different owner remains a deterministic conflict.
- These are software/security-contract facts. Physical captive-portal usability, QR optical scanning at distance, reset/reboot/radio recovery, and end-to-end consumer timing remain separate HIL evidence.

### Voice mail

- Voice-mail metadata is PostgreSQL-authoritative and media is stored through a replaceable blob boundary; the current adapter is local filesystem Ogg Opus.
- Delivery/recovery is durable, privacy-scoped and idempotent.
- Receiver UX queues notifications and requires explicit playback; voice mail never auto-plays.
- Object-store backends such as S3-compatible storage are future deployment adapters, not current product facts.

### Control plane

- Device twins maintain separate desired/reported state and versions.
- Runtime settings apply/report traffic uses `device.settings_v1@1` over the canonical capability plane; the legacy Product-v1 `config.update` / `config.report` transport is not active.
- PostgreSQL remains authoritative for desired/reported ordering and reconciliation. A sent settings command is not proof that the device applied it.
- Scoped configuration, feature metadata, entitlements, privacy policy, enrolled credentials and signed firmware manifests are implemented backend/control-plane boundaries.
- Signed OTA metadata/control-plane support does **not** imply the device-side A/B apply/health/rollback lifecycle is complete.

## Evidence boundaries

Code existence is not proof of provider or physical quality. Use [`docs/TEST_EVIDENCE_LADDER.md`](docs/TEST_EVIDENCE_LADDER.md):

```text
Tier 0 — deterministic host / contract tests
    ↓
Tier 1 — production C++ software device against real Go backend
    ↓
Tier 2 — targeted simulation where the simulator represents the behavior
    ↓
Tier 3 — trusted physical HIL for RF/audio/display/power/OTA/peripheral claims
```

Current evidence deliberately keeps these concerns separate:

- PostgreSQL/Atlas/River software/data-plane behavior has hosted evidence.
- Protocol/session/ToolRegistry/device-capability orchestration has deterministic/Tier-1 evidence for the implemented paths. Capability hardening is covered by exact-head Go/host/ESP32/Tier-1 merge gates; physical volume effect remains unproven because physical firmware does not advertise volume.
- Reference ASR/TTS/realtime/model adapters exist, but Production-v1 real VN/EN provider/model selection still requires measured evidence.
- ESP-SR software integration exists, while enclosure AEC/wake/false-interruption/resource behavior remains physical qualification work.
- Hardware/display selection remains purchase/physical-benchmark gated.
- A successful build, mock, software-device run or simulator cannot be relabeled as physical/provider proof.

Machine-readable promoted claims live in [`evidence/status.json`](evidence/status.json). Conservative `unproven`/`partial` status is preferable to inferring a PASS from implementation alone.

## Product scope

Production v1 focuses on:

- Vietnamese/English realtime voice interaction with interruption;
- expenses and budgets;
- notes, diary and personal memory;
- reminders and timers;
- voice memos and explicit-playback voice mail;
- typed server-driven presentation state;
- policy-controlled native/device/external tools;
- versioned configuration, feature metadata and signed OTA control-plane state;
- local/offline-first behavior only where the measured selected runtime actually supports it.

Non-goals include premature Kubernetes/microservices, direct unrestricted Internet/tool access from the LLM, MCP on firmware, arbitrary executable remote UI, and permanent duplicate product runtimes kept only as fallback.

## Quick start

```bash
# 1. Configure API credentials (required — backend won't start without them)
cp .env.example .env
$EDITOR .env          # set ADK_OPENAI_BASE_URL, ADK_MODEL, ADK_OPENAI_API_KEY

# 2. Launch the stack (PostgreSQL, migrations, backend daemon)
./scripts/run_app.sh
```

- 🌐 **Owner Web Dashboard:** [http://localhost:8000/v1/owner/dashboard](http://localhost:8000/v1/owner/dashboard)
- 📖 **User Guide & Feature Manual:** [`docs/USER_GUIDE.md`](docs/USER_GUIDE.md)

## Development and verification

Backend production toolchain: **Go 1.26.6**.

`companiond` requires an Atlas-migrated PostgreSQL database via `COMPANION_DATABASE_URL`.

```bash
# Full containerized host + backend regression
make e2e-container

# Backend quality gates
cd backend
go mod verify
go vet -tags "adk,mcp,nolibopusfile" ./...
go test -tags "adk,mcp,nolibopusfile" -race -count=1 ./...
```

Use the nearest direct oracle during implementation and broader exact-head CI only when the change crosses the corresponding boundary. Physical HIL runs only on trusted refs through the manually authorized HIL workflow.

AI-assisted engineering follows [`ai_development_workflow.md`](ai_development_workflow.md): one accountable lead by default, small coherent PRs, bounded delegation, PR descriptions as execution records, and GitHub Checks as hosted proof.

## Where to look

- **Live requirements / work state:** GitHub Issues and PRs. Durable docs do not mirror open-branch queues.
- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — implementation architecture and runtime boundaries.
- [`docs/ADR-003-DEVICE-CAPABILITY-PLANE.md`](docs/ADR-003-DEVICE-CAPABILITY-PLANE.md) — sole Product-v1 device-capability architecture contract.
- [`docs/architecture/AI_COMPANION_RESET_EXECUTION_PLANS_V2_CANONICAL_2026-08-17.md`](docs/architecture/AI_COMPANION_RESET_EXECUTION_PLANS_V2_CANONICAL_2026-08-17.md) — canonical architecture-reset/release execution ledger; refresh GitHub state before mutation.
- [`docs/COMMERCIAL_ARCHITECTURE.md`](docs/COMMERCIAL_ARCHITECTURE.md) — durable commercial/evolution architecture.
- [`docs/TEST_EVIDENCE_LADDER.md`](docs/TEST_EVIDENCE_LADDER.md) — evidence classes and promotion limits.
- [`evidence/status.json`](evidence/status.json) — machine-readable promoted evidence claims.
- [`docs/ADR-001-REPLACEABLE-PROVIDERS.md`](docs/ADR-001-REPLACEABLE-PROVIDERS.md) — provider/adapter boundaries.
- [`docs/OBSERVABILITY.md`](docs/OBSERVABILITY.md) — bounded privacy-safe telemetry contract.
- [`docs/checkpoints/README.md`](docs/checkpoints/README.md) — immutable checkpoint/tag history.
- [`ai_development_workflow.md`](ai_development_workflow.md) — implementation/review/delegation workflow.
- [`.agents/rules/github_issue_generation.md`](.agents/rules/github_issue_generation.md) — ready-issue specification contract.

Historical implementation details remain recoverable from Git history, issues, PRs and commits. Retired phase/status snapshots are not live backlog or architecture truth and should not be recreated as parallel sources of truth.

## Checkpoints and rollback

The latest immutable historical software checkpoint remains **`CP-SW2.3-20260812`**. `main` has moved substantially beyond it. New immutable checkpoints should be created only when the intended scope, exact-head/post-merge gates, independent review and every promoted evidence claim are coherent.

Rollback uses stable interfaces plus Git revert, database restore/versioned migration recovery, provider/config artifact rollback, and ESP-IDF OTA rollback where implemented. Obsolete product runtimes are not retained indefinitely solely as rollback mechanisms.
