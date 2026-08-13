#!/usr/bin/env python3
from pathlib import Path

path = Path("host/companion_software_device/main.cpp")
text = path.read_text()

replacements = [
    (
        'bool probe_v1_rejection(const std::string& host, const std::string& port,\n                        const std::string& token) {',
        'bool probe_v1_rejection(const std::string& host, const std::string& port,\n                        const std::string& token, const std::string& device_id) {',
    ),
    (
        '        request.set("Device-Id", "software-device-negative");\n        request.set("Client-Id", "software-device-negative");',
        '        request.set("Device-Id", device_id);\n        request.set("Client-Id", device_id);',
    ),
    (
        '  std::string token = "tier1-device-token";\n  std::string admin_token = "tier1-admin-token";',
        '  std::string token = "tier1-device-token";\n  std::string device_id = "software-device-tier1";\n  std::string admin_token = "tier1-admin-token";',
    ),
    (
        '    if (arg == "--url") url = value("--url");\n    else if (arg == "--token") token = value("--token");\n    else if (arg == "--admin-token") admin_token = value("--admin-token");',
        '    if (arg == "--url") url = value("--url");\n    else if (arg == "--device-id") device_id = value("--device-id");\n    else if (arg == "--token") token = value("--token");\n    else if (arg == "--admin-token") admin_token = value("--admin-token");',
    ),
    ('DeviceFixture fixture(url, token, "software-device-happy");', 'DeviceFixture fixture(url, token, device_id);'),
    ('DeviceFixture fixture(url, token, "software-device-duplicate");', 'DeviceFixture fixture(url, token, device_id);'),
    ('DeviceFixture fixture(url, token, "software-device-barge");', 'DeviceFixture fixture(url, token, device_id);'),
    ('DeviceFixture fixture(url, token, "software-device-reconnect");', 'DeviceFixture fixture(url, token, device_id);'),
    (
        '    const std::string device = "software-device-config";\n    DeviceFixture fixture(url, token, device);',
        '    const std::string device = device_id;\n    DeviceFixture fixture(url, token, device);',
    ),
    (
        '    require(probe_v1_rejection(host, port, token),',
        '    require(probe_v1_rejection(host, port, token, device_id),',
    ),
    ('DeviceFixture fixture(url, token, "software-device-tool");', 'DeviceFixture fixture(url, token, device_id);'),
    (
        '  const json providers = scenario_set == "tool"\n                             ? json{{"asr", "mock"}, {"agent", "fake_model"}, {"tts", "mock"}}\n                             : json{{"asr", "mock"}, {"agent", "mock"}, {"tts", "mock"}};',
        '  const json providers = json{{"asr", "mock"}, {"agent", "adk_fake_responses"},\n                              {"tts", "mock"}};',
    ),
]

for old, new in replacements:
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"guard failed: expected one match, got {count}: {old[:100]!r}")
    text = text.replace(old, new, 1)

for stale in [
    'software-device-happy',
    'software-device-duplicate',
    'software-device-barge',
    'software-device-reconnect',
    'software-device-config',
    'software-device-negative',
    'software-device-tool',
]:
    if stale in text:
        raise SystemExit(f"stale per-scenario device id remains: {stale}")

path.write_text(text)
