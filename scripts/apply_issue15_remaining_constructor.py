#!/usr/bin/env python3
from pathlib import Path
p = Path('backend/internal/server/server_test.go')
s = p.read_text()
old = 'service := newAuthenticatedTestServer(pipeline.Components{ASR: pipeline.MockASR{}, Agent: pipeline.MockAgent{}, TTS: pipeline.MockTTS{}, Codecs: pipeline.OpusFactory{}}, "device-token", slog.New(slog.NewTextHandler(io.Discard, nil)), WithFirmwareService(firmware), WithAdminToken("admin-token"))'
new = 'service := newAuthenticatedTestServer(pipeline.Components{ASR: pipeline.MockASR{}, Agent: pipeline.MockAgent{}, TTS: pipeline.MockTTS{}, Codecs: pipeline.OpusFactory{}}, WithFirmwareService(firmware), WithAdminToken("admin-token"))'
if s.count(old) != 1:
    raise SystemExit(f'guard failed: {s.count(old)}')
p.write_text(s.replace(old, new, 1))
