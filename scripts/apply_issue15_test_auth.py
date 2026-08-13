#!/usr/bin/env python3
from pathlib import Path

path = Path('backend/internal/server/server_test.go')
text = path.read_text()

# Add domain import used by the explicit test authenticator.
old = '\t"companion-server/internal/controlplane"\n\tconversationctx "companion-server/internal/conversation"\n'
new = '\t"companion-server/internal/controlplane"\n\tconversationctx "companion-server/internal/conversation"\n\t"companion-server/internal/domain"\n'
if text.count(old) != 1:
    raise SystemExit('import guard failed')
text = text.replace(old, new, 1)

# Add explicit authenticated test boundary. Product auth remains fail-closed.
anchor = 'var testEnvelopeSequence atomic.Uint64\n\n'
helper = '''var testEnvelopeSequence atomic.Uint64

type testDeviceAuthenticator struct{}

func (testDeviceAuthenticator) AuthenticateDevice(_ context.Context, deviceID, credential string) (domain.Identity, bool, error) {
\tif strings.TrimSpace(deviceID) == "" || credential != "test-device-credential" {
\t\treturn domain.Identity{DeviceID: deviceID}, false, nil
\t}
\treturn domain.Identity{UserID: "default", DeviceID: deviceID}, true, nil
}

func newAuthenticatedTestServer(components pipeline.Components, options ...Option) *Server {
\toptions = append(options, WithDeviceAuthenticator(testDeviceAuthenticator{}))
\treturn New(components, slog.New(slog.NewTextHandler(io.Discard, nil)), options...)
}

func testDeviceDialOptions(deviceID string) *websocket.DialOptions {
\theaders := http.Header{}
\theaders.Set("Device-Id", deviceID)
\theaders.Set("Authorization", "Bearer test-device-credential")
\treturn &websocket.DialOptions{HTTPHeader: headers}
}

'''
if text.count(anchor) != 1:
    raise SystemExit('helper anchor guard failed')
text = text.replace(anchor, helper, 1)

# Convert all six server constructor sites in this file to the explicit-auth helper.
text = text.replace('service := New(pipeline.Components{', 'service := newAuthenticatedTestServer(pipeline.Components{')
# Old constructor logger/token tails; options are preserved.
for old_tail, new_tail in [
    ('\t}, "", slog.New(slog.NewTextHandler(io.Discard, nil)))', '\t})'),
    ('\t}, "", slog.New(slog.NewTextHandler(io.Discard, nil)), WithStore(repo), WithSchedulerInterval(20*time.Millisecond))', '\t}, WithStore(repo), WithSchedulerInterval(20*time.Millisecond))'),
    ('\t}, "", slog.New(slog.NewTextHandler(io.Discard, nil)), WithStore(data), WithLocation(location))', '\t}, WithStore(data), WithLocation(location))'),
]:
    text = text.replace(old_tail, new_tail)
text = text.replace('service := New(pipeline.Components{ASR: pipeline.MockASR{}, Agent: pipeline.MockAgent{}, TTS: pipeline.MockTTS{}, Codecs: pipeline.OpusFactory{}}, "device-token", slog.New(slog.NewTextHandler(io.Discard, nil)), WithFirmwareService(firmware), WithAdminToken("admin-token"))',
                    'service := newAuthenticatedTestServer(pipeline.Components{ASR: pipeline.MockASR{}, Agent: pipeline.MockAgent{}, TTS: pipeline.MockTTS{}, Codecs: pipeline.OpusFactory{}}, WithFirmwareService(firmware), WithAdminToken("admin-token"))')

# Every WebSocket integration connection authenticates explicitly.
replacements = [
    ('connection, _, err := websocket.Dial(ctx, url, nil)', 'connection, _, err := websocket.Dial(ctx, url, testDeviceDialOptions("device-test"))'),
    ('headers := http.Header{}\n\theaders.Set("Device-Id", "legacy-device")\n\tconnection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v2/device", &websocket.DialOptions{HTTPHeader: headers})',
     'connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v2/device", testDeviceDialOptions("legacy-device"))'),
    ('headers := http.Header{}\n\theaders.Set("Device-Id", "device-test")\n\tconnection, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: headers})',
     'connection, _, err := websocket.Dial(ctx, url, testDeviceDialOptions("device-test"))'),
    ('headers := http.Header{}\n\theaders.Set("Device-Id", "device-expense-e2e")\n\tconnection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v2/device", &websocket.DialOptions{HTTPHeader: headers})',
     'connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/v2/device", testDeviceDialOptions("device-expense-e2e"))'),
    ('conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/v2/device", nil)',
     'conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/v2/device", testDeviceDialOptions("stream-device"))'),
]
for old_value, new_value in replacements:
    count = text.count(old_value)
    if count != 1:
        raise SystemExit(f'dial guard failed count={count}: {old_value[:80]}')
    text = text.replace(old_value, new_value, 1)

path.write_text(text)

# Fix composition-root call to the new fail-closed server constructor.
main = Path('backend/cmd/companiond/main.go')
main_text = main.read_text()
old_call = 'service := server.New(components, "", logger, serverOptions...)'
if main_text.count(old_call) != 1:
    raise SystemExit('companiond server.New guard failed')
main.write_text(main_text.replace(old_call, 'service := server.New(components, logger, serverOptions...)', 1))
