# Production readiness — retired duplicated matrix

> **Not a live status source.** The previous table copied implementation and evidence state into Markdown and became stale (for example, it still described SQLite as product authority after the PostgreSQL hard cut). It is retired rather than continually re-synchronized by hand.

## Canonical readiness sources

Production readiness is derived from separate sources that own different facts:

- `main` code/schema/config — what is actually merged.
- GitHub Issues — remaining requirement/acceptance work.
- PR descriptions — what a change implemented and intentionally did not prove.
- GitHub Checks/artifacts — automated proof for exact reviewed code.
- [`../evidence/status.json`](../evidence/status.json) — promoted evidence claims and explicit `partial` / `unproven` boundaries.
- [`TEST_EVIDENCE_LADDER.md`](TEST_EVIDENCE_LADDER.md) — what each evidence tier is allowed to prove.
- [`../README.md`](../README.md) and architecture/ADR docs — durable merged product/architecture explanation, not live work queues.

Do not recreate a second readiness dashboard in this file.

## Readiness rule

A production claim must have the evidence appropriate to the claim:

```text
implemented code
    != real-provider proof
    != physical-HIL proof
    != operational/release proof
```

Examples:

- PostgreSQL/Atlas/River software behavior can be promoted from real database/hosted integration evidence.
- ASR/TTS/model quality requires real-provider/model benchmark evidence.
- Wake/AEC/display/RF/power/OTA physical behavior requires trusted hardware evidence.
- A mock, build, source inspection or simulator does not promote a higher-tier physical/provider claim.

## Historical content

The previous readiness matrix is available in Git history for audit purposes. Any current planning decision must use the canonical sources above rather than that historical snapshot.
