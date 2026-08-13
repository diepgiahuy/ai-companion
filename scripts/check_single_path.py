#!/usr/bin/env python3
from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parents[1]

FORBIDDEN_PATHS = [
    "backend/internal/agent",
    "backend/go.offline.mod",
    "backend/offline_deps",
    "backend/internal/webrtcbridge",
    "host/companion_software_device/fake_qwen.py",
]

# Active product/config/build surfaces only. Historical checkpoints are
# intentionally not scanned: they document prior states and are not runtime
# instructions.
SCAN_ROOTS = [
    "README.md",
    "backend/README.md",
    "backend/config.example.env",
    "backend/cmd",
    "backend/internal/adkbridge",
    "backend/internal/runtimeconfig",
    "backend/internal/server",
    "main",
    "components/esp32_network",
    "host/companion_software_device",
    "Makefile",
    ".github/workflows",
    "docs/ARCHITECTURE.md",
]

FORBIDDEN_TOKENS = [
    "COMPANION_AGENT_RUNTIME",
    "COMPANION_DEVICE_AUTH",
    "COMPANION_DEVICE_TOKEN",
    "CONFIG_COMPANION_USE_WEBSOCKET",
    "CONFIG_COMPANION_DEVICE_TOKEN",
    "QWEN_BASE_URL",
    "QWEN_API_KEY",
    "QWEN_MODEL",
    "QWEN_FAST_MODEL",
    "QWEN_REASONING_MODEL",
    "fake_qwen",
    "go.offline.mod",
    "offline_deps",
    "webrtcbridge",
]

FORBIDDEN_PATTERNS = [
    re.compile(r"adk,mcp,webrtc(?:,|\b)", re.IGNORECASE),
    re.compile(r"WebRTC\s*/\s*Opus\s*\(parallel", re.IGNORECASE),
    re.compile(r"WebRTC Opus bridge", re.IGNORECASE),
    re.compile(r"legacy provider adapter", re.IGNORECASE),
]


def iter_files(path: Path):
    if path.is_file():
        yield path
        return
    if not path.exists():
        return
    for child in path.rglob("*"):
        if not child.is_file():
            continue
        if child.suffix.lower() in {".png", ".jpg", ".jpeg", ".gif", ".zip", ".bin"}:
            continue
        yield child


failures = []
for rel in FORBIDDEN_PATHS:
    if (ROOT / rel).exists():
        failures.append(f"forbidden legacy path exists: {rel}")

seen = set()
for rel in SCAN_ROOTS:
    for path in iter_files(ROOT / rel):
        if path in seen:
            continue
        seen.add(path)
        try:
            text = path.read_text(encoding="utf-8")
        except UnicodeDecodeError:
            continue
        display = path.relative_to(ROOT)
        for token in FORBIDDEN_TOKENS:
            if token in text:
                failures.append(f"{display}: forbidden active marker {token!r}")
        for pattern in FORBIDDEN_PATTERNS:
            if pattern.search(text):
                failures.append(f"{display}: forbidden active pattern {pattern.pattern!r}")

# Positive invariants make the gate fail if the canonical path is accidentally
# removed while legacy markers remain absent.
required = {
    "backend/config.example.env": ["ADK_OPENAI_BASE_URL", "ADK_MODEL"],
    "components/esp32_network/Kconfig.projbuild": ["COMPANION_DEVICE_CREDENTIAL"],
    "backend/cmd/companiond/main.go": ["WithDeviceAuthenticator(data)", "ADK_OPENAI_BASE_URL", "ADK_MODEL"],
}
for rel, needles in required.items():
    path = ROOT / rel
    if not path.is_file():
        failures.append(f"required single-path file missing: {rel}")
        continue
    text = path.read_text(encoding="utf-8")
    for needle in needles:
        if needle not in text:
            failures.append(f"{rel}: required single-path marker missing: {needle!r}")

if failures:
    print("SINGLE PATH GATE FAIL", file=sys.stderr)
    for failure in failures:
        print(f"- {failure}", file=sys.stderr)
    raise SystemExit(1)

print(f"SINGLE PATH GATE PASS: scanned {len(seen)} active files; no legacy runtime/auth/transport path remains")
