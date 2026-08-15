# Observability contract — Production v1

Status: implementation contract for issue #25. This document defines stable Companion-owned semantics; it does **not** select a hosted telemetry vendor and it does not promote any production evidence gate by itself.

## Principles

1. Product/runtime code emits a small typed event schema owned by Companion.
2. Realtime and domain mutation paths never perform exporter network/disk I/O.
3. Recording is bounded and non-blocking. Contention/capacity becomes a counted drop, never backpressure on audio or a tool mutation.
4. Session/turn identifiers are trace correlation fields only. Raw protocol identifiers are pseudonymized before every Recorder and are forbidden as unbounded metric dimensions.
5. Raw transcript, audio, tool arguments/results, credentials, user IDs, device IDs, file contents and arbitrary provider payloads are not observability fields.
6. A metric/event existing is not evidence promotion. Evidence still requires commit + config/scenario + environment/provider class + result.

## Schema

The in-process schema is `backend/internal/observability.Event`, currently `schema_version=1`.

Stable event names:

| Event | Meaning | Required safe fields |
|---|---|---|
| `session.ready` | authenticated v2 session completed hello/ready | session correlation |
| `session.end` | device session terminated | session correlation, duration, bounded outcome |
| `turn.start` | new generation-scoped turn accepted | session/turn/generation |
| `turn.stage` | one ASR/agent/TTS stage completed | correlation, stage, duration, bounded outcome |
| `turn.end` | turn completed/failed | correlation, total duration, bounded outcome/reason |
| `turn.interrupted` | barge-in/client abort invalidated a generation | correlation, bounded reason |
| `queue.full` | bounded output lane rejected work | session/generation where available, queue, configured capacity |
| `tool.end` | ToolRegistry execution returned | correlation, registered tool name or `unsupported`, risk class, duration, bounded outcome |

`duration_ms` uses monotonic Go durations converted to integer milliseconds. `at` is UTC RFC3339 JSON time and is for ordering/evidence only; latency is never calculated from wall-clock subtraction.

Allowed outcome vocabulary for v1 is intentionally small: `ok`, `error`, `cancelled`. Reasons/stages are controlled code constants, not raw error messages.

## Cardinality policy

Safe bounded dimensions for aggregation:

- event name;
- stage (`asr`, `agent`, `agent_stream`, `tts`, `tts_stream`, `first_segment`);
- outcome;
- queue (`control`, `audio`);
- tool name **only after registry resolution**; unsupported arbitrary model strings collapse to `unsupported`;
- tool risk (`read`, `write`, `destructive`, `external`, empty/unknown);
- provider/model/config fingerprints only when a later adapter supplies a bounded configured value.

Never metric-label by session ID, turn ID, generation ID, user/device/tenant/thread ID, transcript, URI, arbitrary error text, tool arguments or resource identifiers.

## Correlation and privacy

`Correlation{session_id, turn_id, generation_id}` is attached to trace/evidence events. Runtime code may temporarily carry the protocol/session identifiers so downstream ToolRegistry execution belongs to the same turn, but `RecordTo` is the privacy boundary: it pseudonymizes `session_id` and `turn_id` **before any Recorder receives the event**.

Pseudonyms use a process-local HMAC-SHA256 secret obtained from OS entropy and are serialized as `c_<32 hex>`. The secret is never persisted or exported. Turn IDs are namespaced by the raw session identifier before hashing, so reusing the same client-controlled turn string in another session does not create a stable cross-session identifier. `generation_id` is server-owned and bounded.

This placement is intentional: future Ring/OTel/vendor Recorders cannot accidentally bypass correlation privacy. Tier-1 and unit tests reject raw correlation identifiers and require the pseudonym format.

Future database/River/firmware instrumentation should extend the same contract rather than create a second telemetry stack. Cross-process trace propagation may later add a bounded opaque trace ID, but raw W3C/OTel SDK types must stay outside domain packages.

## Recorder/exporter boundary

`Recorder.TryRecord(Event) bool` is the product boundary. Implementations must return immediately. The first implementation is `RingRecorder`:

- fixed capacity;
- `sync.Mutex.TryLock` so contention drops rather than waits;
- no overwrite of prior evidence;
- atomic dropped-event counter;
- snapshot serialization happens only outside the realtime path.

`RecordTo` also contains Recorder panics so telemetry cannot turn a successful voice/tool/domain path into a product failure.

`COMPANION_OBSERVABILITY_FILE` enables a CI/development snapshot on graceful shutdown. No path means a no-op recorder. A future OTel/vendor exporter should consume snapshots/events behind this boundary; provider exporter failure must not alter application semantics.

## Tier-1 evidence

`host/companion_software_device` enables the same product recorder and validates its JSON snapshot. Tier-1 requires:

- pseudonymized core session/turn correlation;
- non-negative ASR/agent/TTS/total timing events;
- representative tool events for the ADK tool parity cases;
- zero recorder drops in deterministic Tier-1 runs;
- absence of forbidden private/raw fields;
- active barge-in cancellation rather than a timing race against an instant mock TTS loop.

These snapshots remain `tier1_orchestration` evidence. Mock ASR/TTS and deterministic Responses fixtures cannot promote real ASR/TTS/LLM or physical-device gates.

### Verified deterministic evidence

GitHub Actions `software-device-e2e` run **31708039890** validated the final pseudonymized contract on source head `7ae065f6f8fb73cde8ee71087806d0189634406f` (PR merge test commit `d6b8ddcdd24c62d4291d188a20ee9bade1b90dfa`). Artifact **9184091869** has digest `sha256:dca97e0b5623611bd9e0689555b581de94e738def9fb32d81599b1a07891c808`.

The run proved the production CompanionApp/companiond/ADK Responses orchestration path emitted valid pseudonymized core timing/correlation plus `tool.end` events for `expense.log`, `budget.set`, `note.create`, `journal.create`, `reminder.create`, `timer.create`, and `memory.remember`, with zero deterministic recorder drops. It also passed the active-turn barge-in scenario after the deterministic MockTTS fixture was changed to use a small context-cancellable inter-frame delay instead of an instantaneous synchronous loop.

This remains orchestration-only evidence; it is not a real-provider latency benchmark or physical HIL result.

## Downstream metric calculations

#17/#18 can compute reproducible p50/p95 only from a homogeneous evidence set sharing scenario, provider/model, config fingerprint and environment class. Do not mix hosted CI deterministic timings with real-provider/network timings.

At minimum later real-provider evidence should derive:

- ASR final latency;
- model/agent first speakable segment and total latency;
- TTS first-audio/active duration;
- end-to-end turn latency;
- cancellation/barge-in latency;
- queue-full/drop counts;
- tool duration/outcome by bounded registered name/risk.

River retention exposes bounded aggregate enqueue, unique-skip, completion, retry, failure, cancellation, queue-wait and run-duration metrics through the admin-token endpoint. Job IDs, user data, paths and raw errors are excluded. #3 can add HIL resource snapshots (heap/PSRAM/reset/watchdog) while keeping physical evidence classification separate.

## Retention

CI observability snapshots use synthetic test data and inherit the workflow artifact retention. Product deployments must choose a bounded retention period before enabling a persistent exporter. The default product configuration does not persist observability events.
