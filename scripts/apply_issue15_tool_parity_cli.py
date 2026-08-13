#!/usr/bin/env python3
from pathlib import Path

path = Path("host/companion_software_device/main.cpp")
text = path.read_text()
replacements = [
    (
        '  std::string admin_token = "tier1-admin-token";\n  std::string evidence_path = "software-device-evidence.json";',
        '  std::string admin_token = "tier1-admin-token";\n  std::string expected_text = "Tier-1 tool parity ok";\n  std::string evidence_path = "software-device-evidence.json";',
    ),
    (
        '    else if (arg == "--admin-token") admin_token = value("--admin-token");\n    else if (arg == "--evidence") evidence_path = value("--evidence");',
        '    else if (arg == "--admin-token") admin_token = value("--admin-token");\n    else if (arg == "--expected-text") expected_text = value("--expected-text");\n    else if (arg == "--evidence") evidence_path = value("--evidence");',
    ),
    (
        '      require(fixture.display.contains("Đã lưu đúng một khoản 50 nghìn"),\n              "deterministic model/tool response was not rendered");',
        '      require(fixture.display.contains(expected_text),\n              "deterministic model/tool response was not rendered");',
    ),
]
for old, new in replacements:
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"guard failed count={count}: {old[:100]!r}")
    text = text.replace(old, new, 1)
path.write_text(text)
