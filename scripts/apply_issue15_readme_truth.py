#!/usr/bin/env python3
from pathlib import Path

path = Path("README.md")
text = path.read_text()
replacements = [
    ('| WebRTC real-network latency | ⚪ unproven | Must measure p50/p95 first-audio, loss recovery and barge-in |\n', ''),
    (
        '| Tier-1 headless software device | 🟡 partial | Real Go `/v2/device` + production `CompanionApp`/protocol v2; six core scenarios plus one deterministic agent→tool→SQLite mutation pass; no provider/physical promotion |',
        '| Tier-1 headless software device | 🟡 partial | Real Go `/v2/device` + production `CompanionApp`/protocol v2 + ADK Responses adapter; core scenarios, enrolled-auth lifecycle and representative expense/budget/note/journal/reminder/timer/memory mutations are deterministic orchestration gates; no provider/physical promotion |',
    ),
    (
        '- **Typed runtime config** — LLM generation parameters and runtime profile are validated; production profile rejects mock-provider fallback.\n- **Semantic model routing** — embedding/prototype router replaces compiled `strings.Contains` keyword routing.\n',
        '- **Typed runtime config** — the runtime profile and live ADK settings are validated; production profile rejects mock providers and missing ADK model/base-URL fails startup.\n',
    ),
    ('- **WebRTC Opus bridge** — Pion WebRTC adapter in parallel with the existing WebSocket transport; latency target remains unproven until measured on real networks.\n', ''),
    (
        '- **Tier-1 headless software device** — production C++ `CompanionApp` + protocol v2 connect to real `companiond` through a host-only WebSocket/libopus adapter; the harness covers reconnect/barge-in/replay/config plus a deterministic production agent→tool→SQLite mutation, while all mock/fake-provider evidence remains `orchestration_only`.',
        '- **Tier-1 headless software device** — production C++ `CompanionApp` + protocol v2 connect to real `companiond` through a host-only WebSocket/libopus adapter; the harness covers reconnect/barge-in/replay/config, wrong/revoked device credentials, ADK tool loops, and representative authoritative mutations for expense/budget/note/journal/reminder/timer/memory. Deterministic providers remain `orchestration_only`.',
    ),
    (
        'Realtime transport\n  ├─ WebSocket (current v2 control + binary Opus transport)\n  └─ WebRTC / Opus (parallel foundation; real-network proof pending)',
        'Realtime transport\n  └─ WebSocket v2 (typed control + binary Opus; sole current product transport)',
    ),
    (
        'Agent runtime\n  ├─ legacy provider adapter\n  ├─ Google ADK anti-corruption layer\n  └─ future local/native realtime adapters',
        'Agent runtime\n  └─ Google ADK v2 anti-corruption layer\n       ├─ OpenAI Responses-compatible model adapter\n       └─ full public ToolRegistry adapters',
    ),
    ('- model router\n', ''),
    ('go vet -tags "adk,mcp,webrtc,nolibopusfile" ./...', 'go vet -tags "adk,mcp,nolibopusfile" ./...'),
    ('go test -tags "adk,mcp,webrtc,nolibopusfile" -race -count=1 ./...', 'go test -tags "adk,mcp,nolibopusfile" -race -count=1 ./...'),
]
for old, new in replacements:
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"README guard failed count={count}: {old[:120]!r}")
    text = text.replace(old, new, 1)
for stale in [
    'WebRTC Opus bridge',
    'WebRTC / Opus (parallel foundation',
    'legacy provider adapter',
    'adk,mcp,webrtc,nolibopusfile',
    'Semantic model routing',
]:
    if stale in text:
        raise SystemExit(f"stale active README claim remains: {stale}")
path.write_text(text)
