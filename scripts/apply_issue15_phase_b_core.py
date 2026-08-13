#!/usr/bin/env python3
from pathlib import Path


def replace_once(text: str, old: str, new: str, label: str) -> str:
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"{label}: expected exactly one old block, found {count}")
    return text.replace(old, new, 1)


main = Path("backend/cmd/companiond/main.go")
text = main.read_text()
for old in [
    '\t"encoding/json"\n',
    '\t"companion-server/internal/agent"\n',
    '\t"companion-server/internal/contextengine"\n',
]:
    text = replace_once(text, old, "", f"main import {old.strip()}")
text = replace_once(
    text,
    '\tvar embedding memory.EmbeddingProvider = memory.HashEmbedding{Dimensions: 96}\n\trealEmbedding := false\n',
    '\tvar embedding memory.EmbeddingProvider = memory.HashEmbedding{Dimensions: 96}\n',
    "embedding real flag",
)
text = replace_once(text, '\n\t\trealEmbedding = true\n', "\n", "embedding configured flag")

start = text.index('\tmonthlyLLMLimit, _ := strconv.ParseInt')
end = text.index('\trootCtx, stop :=', start)
replacement = '''\tmonthlyLLMLimit, _ := strconv.ParseInt(value("COMPANION_MONTHLY_LLM_TOKEN_LIMIT", "0"), 10, 64)
\tusageGuard := usage.Guard{Reader: data, MonthlyLimit: monthlyLLMLimit}
\tadkPrompt, err := promptBundle.Render(promptpkg.RenderInput{
\t\tLocale:      "vi-VN",
\t\tCurrentTime: time.Now().In(location),
\t\tTimezone:    timezone,
\t\tPersona:     runtimeCfg.LLM.Persona,
\t\tPacks:       []string{"expense", "budget", "schedule", "note", "journal", "memory", "market", "voice", "context"},
\t})
\tif err != nil {
\t\tlogger.Error("render ADK prompt", "error", err)
\t\tos.Exit(1)
\t}
\tadkAgent, err := adkbridge.New(adkbridge.Config{
\t\tAppName:         "companion",
\t\tModelName:       value("ADK_MODEL", "Qwen/Qwen3-4B-Instruct-2507"),
\t\tBaseURL:         os.Getenv("ADK_OPENAI_BASE_URL"),
\t\tAPIKey:          os.Getenv("ADK_OPENAI_API_KEY"),
\t\tInstruction:     adkPrompt.Text,
\t\tPromptVersion:   adkPrompt.ID + "@" + adkPrompt.Version + "#" + adkPrompt.Fingerprint,
\t\tHTTPClient:      &http.Client{Timeout: runtimeCfg.LLM.HTTPTimeout},
\t\tTools:           toolRegistry,
\t\tConversation:    conversationService,
\t\tUsageGuard:      usageGuard,
\t\tUsageMeter:      data,
\t\tTemperature:     runtimeCfg.LLM.Temperature,
\t\tMaxOutputTokens: runtimeCfg.LLM.MaxTokens,
\t\tMaxToolRounds:   runtimeCfg.LLM.MaxToolRounds,
\t})
\tif err != nil {
\t\tlogger.Error("initialize ADK runtime", "error", err, "hint", "build with -tags=adk and configure an OpenAI Responses API-compatible provider")
\t\tos.Exit(1)
\t}
\tcomponents.Agent = adkAgent

\tserverOptions := []server.Option{
\t\tserver.WithStore(data), server.WithLocation(location),
\t\tserver.WithIdentityResolver(server.HeaderIdentityResolver{DefaultUserID: value("COMPANION_DEFAULT_USER_ID", "default")}),
\t\tserver.WithControlPlane(control), server.WithFirmwareService(firmwareService), server.WithPrivacyService(privacyService), server.WithFeatureCatalog(featureCatalog), server.WithAdminToken(os.Getenv("COMPANION_ADMIN_TOKEN")),
\t\tserver.WithDeviceAuthenticator(data), server.WithDeviceCredentialManager(data), server.WithEntitlementManager(data),
\t}
\tservice := server.New(components, logger, serverOptions...)
'''
text = text[:start] + replacement + text[end:]
selector_start = text.find("\nfunc buildModelSelector(")
if selector_start < 0:
    raise SystemExit("buildModelSelector: old function not found")
value_start = text.find("\nfunc value(", selector_start)
if value_start < 0:
    raise SystemExit("buildModelSelector: value sentinel not found")
text = text[:selector_start] + text[value_start:]
main.write_text(text)

runtime = Path("backend/internal/runtimeconfig/config.go")
text = runtime.read_text()
text = replace_once(text, '\tRouter            string\n\tRouterExamplesFile string\n', "", "runtime router fields")
router_start = text.index('\n\trouter := strings.ToLower(strings.TrimSpace(env("COMPANION_MODEL_ROUTER", "static")))')
mock_start = text.index('\n\tdefaultMock := "true"', router_start)
text = text[:router_start] + text[mock_start:]
text = replace_once(text, '\t\t\tRouter:            router,\n\t\t\tRouterExamplesFile: routerExamples,\n', "", "runtime router return")
runtime.write_text(text)

server = Path("backend/internal/server/server.go")
text = server.read_text()
text = replace_once(text, '\ttoken             string\n', "", "server token field")
text = replace_once(
    text,
    "func New(components pipeline.Components, token string, logger *slog.Logger, options ...Option) *Server {",
    "func New(components pipeline.Components, logger *slog.Logger, options ...Option) *Server {",
    "server constructor signature",
)
text = replace_once(text, '\t\tcomponents: components, token: token, logger: logger,\n', '\t\tcomponents: components, logger: logger,\n', "server constructor token init")
old_auth = '''func (s *Server) authenticateDeviceRequest(ctx context.Context, request *http.Request) (domain.Identity, bool) {
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
'''
new_auth = '''func (s *Server) authenticateDeviceRequest(ctx context.Context, request *http.Request) (domain.Identity, bool) {
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
'''
text = replace_once(text, old_auth, new_auth, "server auth fallback")
old_identity = '''\tidentity := s.identityResolver.Resolve(request, authenticated.DeviceID)
\tif s.deviceAuth != nil {
\t\t// In database-auth mode ownership/tenant/plan are trusted enrollment claims,
\t\t// never client-controlled transport headers. Thread remains a conversation concern.
\t\tidentity.UserID = authenticated.UserID
\t\tidentity.TenantID = authenticated.TenantID
\t\tidentity.Plan = authenticated.Plan
\t\tidentity.DeviceID = authenticated.DeviceID
\t}
'''
new_identity = '''\tidentity := s.identityResolver.Resolve(request, authenticated.DeviceID)
\t// Ownership/tenant/plan are trusted enrollment claims, never client-controlled
\t// transport headers. Thread remains a conversation concern.
\tidentity.UserID = authenticated.UserID
\tidentity.TenantID = authenticated.TenantID
\tidentity.Plan = authenticated.Plan
\tidentity.DeviceID = authenticated.DeviceID
'''
text = replace_once(text, old_identity, new_identity, "server authenticated identity binding")
server.write_text(text)

identity = Path("backend/internal/server/identity.go")
text = identity.read_text()
text = replace_once(
    text,
    "// IdentityResolver keeps transport authentication/identity mapping replaceable.\n// The POC resolver accepts optional headers for tests and falls back to a configurable single-user owner plus a per-device thread.\n// Production should replace this with a resolver backed by enrolled device credentials.\n",
    "// IdentityResolver supplies conversation-scoped transport metadata. Authenticated owner, tenant, plan and device claims are always overwritten from the enrolled per-device credential before a session starts.\n",
    "identity stale production comment",
)
identity.write_text(text)
