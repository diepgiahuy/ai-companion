# Checkpoints and rollback history

This directory indexes immutable production checkpoints. The root `README.md` is the human-readable current source of truth; `evidence/status.json` is the machine-verifiable backing for production claims.

A checkpoint is created only after its stated gates pass, an independent static review is recorded, regression tests are rerun on the reviewed code, and rollback metadata is known.

## Software lineage

| Tag | Scope | Status / note |
|---|---|---|
| `CP0-20260812` | Frozen production rewrite baseline | Historical baseline |
| `CP-SW1-20260812` | Realtime turn runtime / streaming foundation | Passed checkpoint |
| `CP-SW2.1-20260812` | Google ADK anti-corruption seam | Partial integration checkpoint at creation time |
| `CP-SW2.2-20260812` | Tool-loop, media ordering and safety hardening | Passed static-review checkpoint |
| `CP-SW2.3-20260812` | Exact Go 1.26.5 production dependency lock | Latest stable software checkpoint |
| `CP-SW2.4-20260816` | Temporal correctness, personal-data queries & Notion dashboard | Active work checkpoint |

Older tags such as `CP4.1-20260812` and `CP5.1-20260812` are retained for audit/history but predate the current CP-SW naming scheme.

## Active work

PR #1 is not a production checkpoint yet. It contains foundations for:

- evidence-backed CI/CD,
- prompt bundle/versioning,
- typed runtime configuration,
- semantic model routing,
- UI state payloads,
- smart-turn primitives,
- destructive authorization scopes,
- native MCP integration,
- Pion WebRTC Opus transport.

Real provider/network/HIL gates remain `unproven` until matching evidence is attached.

## Checkpoint acceptance

Every new checkpoint must include:

1. Scope and architecture impact.
2. Problems/root cause discovered.
3. Solution and trade-offs.
4. Deterministic tests.
5. Real dependency/provider/HIL evidence where required.
6. Independent static review findings and fixes.
7. Post-review regression rerun.
8. Rollback target.
9. Immutable Git tag.
10. `evidence/status.json` update for every production claim changed by the checkpoint.

Mock/fake evidence may support implementation tests but cannot promote a production gate to `passed`.
