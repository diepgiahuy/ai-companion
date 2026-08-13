#!/usr/bin/env python3
from pathlib import Path

path = Path("backend/internal/server/server_test.go")
text = path.read_text()
replacements = [
    (
        'get.Header.Set("Authorization", "Bearer device-token")',
        'get.Header.Set("Device-Id", "ota-device")\n\tget.Header.Set("Authorization", "Bearer test-device-credential")',
    ),
    (
        'get2.Header.Set("Authorization", "Bearer device-token")',
        'get2.Header.Set("Device-Id", "ota-device")\n\tget2.Header.Set("Authorization", "Bearer test-device-credential")',
    ),
]
for old, new in replacements:
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"guard failed count={count}: {old}")
    text = text.replace(old, new, 1)
if 'Bearer device-token' in text:
    raise SystemExit('stale OTA shared device-token fixture remains')
path.write_text(text)
