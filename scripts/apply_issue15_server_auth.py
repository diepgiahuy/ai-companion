#!/usr/bin/env python3
from pathlib import Path

path = Path('backend/internal/server/server.go')
text = path.read_text()
replacements = [
    ('\ttoken             string\n', ''),
    ('func New(components pipeline.Components, token string, logger *slog.Logger, options ...Option) *Server {',
     'func New(components pipeline.Components, logger *slog.Logger, options ...Option) *Server {'),
    ('\t\tcomponents: components, token: token, logger: logger,\n',
     '\t\tcomponents: components, logger: logger,\n'),
    ('''func (s *Server) authenticateDeviceRequest(ctx context.Context, request *http.Request) (domain.Identity, bool) {
\tdeviceID := strings.TrimSpace(request.Header.Get("Device-Id"))
\tif s.deviceAuth != nil {
\t\traw := strings.TrimSpace(strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer "))
\t\tif deviceID == "" || raw == "" {
\t\t\treturn domain.Identity{DeviceID: deviceID}, false
\t\t}
\t\tidentity, ok, err := s.deviceAuth.AuthenticateDevice(ctx, deviceID, raw)
\t\treturn identity, err == nil && ok
\t}
\tif s.token != "" && request.Header.Get("Authorization") != "Bearer "+s.token {
\t\treturn domain.Identity{DeviceID: deviceID}, false
\t}
\treturn domain.Identity{DeviceID: deviceID}, true
}
''', '''func (s *Server) authenticateDeviceRequest(ctx context.Context, request *http.Request) (domain.Identity, bool) {
\tdeviceID := strings.TrimSpace(request.Header.Get("Device-Id"))
\tif s.deviceAuth == nil {
\t\treturn domain.Identity{DeviceID: deviceID}, false
\t}
\traw := strings.TrimSpace(strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer "))
\tif deviceID == "" || raw == "" {
\t\treturn domain.Identity{DeviceID: deviceID}, false
\t}
\tidentity, ok, err := s.deviceAuth.AuthenticateDevice(ctx, deviceID, raw)
\treturn identity, err == nil && ok
}
'''),
]
for old, new in replacements:
    count = text.count(old)
    if count != 1:
        raise SystemExit(f'guard failed: expected exactly one match, got {count}: {old[:80]!r}')
    text = text.replace(old, new, 1)
path.write_text(text)
