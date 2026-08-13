#!/usr/bin/env python3
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def replace_once(rel: str, old: str, new: str) -> None:
    path = ROOT / rel
    text = path.read_text(encoding="utf-8")
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"{rel}: expected one guarded match, found {count}\n--- needle ---\n{old[:500]}")
    path.write_text(text.replace(old, new), encoding="utf-8")


# ToolRegistry is the single authoritative execution point. Only registered
# tool names/risk classes are recorded; arbitrary unsupported names collapse to
# a bounded marker and raw args/results are never telemetry fields.
replace_once(
    "backend/internal/capability/tool.go",
    '''import (\n\t"context"\n\t"encoding/json"\n\t"fmt"\n\t"sort"\n\t"strings"\n\t"sync"\n)''',
    '''import (\n\t"context"\n\t"encoding/json"\n\t"fmt"\n\t"sort"\n\t"strings"\n\t"sync"\n\t"time"\n\n\t"companion-server/internal/observability"\n)''',
)
replace_once(
    "backend/internal/capability/tool.go",
    '''func (r *ToolRegistry) Execute(ctx context.Context, name string, req ToolRequest) (result ToolResult) {\n\t// Tool implementations are extension boundaries. A panic in one tool must\n\t// not tear down the voice/session process or leak the panic payload back to\n\t// the model. Observability can attach richer internal diagnostics later; the\n\t// model-facing failure remains deliberately generic.\n\tdefer func() {\n\t\tif recover() != nil {\n\t\t\tresult = Failure(fmt.Errorf("internal tool execution failed"))\n\t\t}\n\t}()\n\n\tr.mu.RLock()\n\tt := r.tools[name]\n\ta := r.authorizer\n\tr.mu.RUnlock()\n\tif t == nil {\n\t\treturn Failure(fmt.Errorf("unsupported tool %q", name))\n\t}\n\tif d := t.Definition(); d != nil {\n\t\tif err := ValidateArguments(d.Parameters, req.Arguments); err != nil {\n\t\t\treturn Failure(fmt.Errorf("tool arguments rejected: %w", err))\n\t\t}\n\t\tif a != nil {\n\t\t\tif err := a.Authorize(ctx, *d, req); err != nil {\n\t\t\t\treturn Failure(fmt.Errorf("tool denied: %w", err))\n\t\t\t}\n\t\t}\n\t}\n\treturn t.Execute(ctx, req)\n}''',
    '''func (r *ToolRegistry) Execute(ctx context.Context, name string, req ToolRequest) (result ToolResult) {\n\tstarted := time.Now()\n\trecordedName := "unsupported"\n\trisk := ""\n\tdefer func() {\n\t\tif recover() != nil {\n\t\t\tresult = Failure(fmt.Errorf("internal tool execution failed"))\n\t\t}\n\t\toutcome := "error"\n\t\tvar marker struct {\n\t\t\tOK bool `json:"ok"`\n\t\t}\n\t\tif json.Unmarshal([]byte(result.Content), &marker) == nil && marker.OK {\n\t\t\toutcome = "ok"\n\t\t}\n\t\tobservability.Record(ctx, observability.Event{\n\t\t\tName: observability.EventToolEnd, DurationMS: time.Since(started).Milliseconds(),\n\t\t\tOutcome: outcome, ToolName: recordedName, ToolRisk: risk,\n\t\t})\n\t}()\n\n\tr.mu.RLock()\n\tt := r.tools[name]\n\ta := r.authorizer\n\tr.mu.RUnlock()\n\tif t == nil {\n\t\treturn Failure(fmt.Errorf("unsupported tool %q", name))\n\t}\n\trecordedName = t.Name()\n\tif d := t.Definition(); d != nil {\n\t\trisk = d.Risk\n\t\tif err := ValidateArguments(d.Parameters, req.Arguments); err != nil {\n\t\t\treturn Failure(fmt.Errorf("tool arguments rejected: %w", err))\n\t\t}\n\t\tif a != nil {\n\t\t\tif err := a.Authorize(ctx, *d, req); err != nil {\n\t\t\t\treturn Failure(fmt.Errorf("tool denied: %w", err))\n\t\t\t}\n\t\t}\n\t}\n\treturn t.Execute(ctx, req)\n}''',
)

# Server/session instrumentation. Recorder is instance-scoped, defaults to NOP,
# and correlation carries no user/device/content fields.
replace_once(
    "backend/internal/server/server.go",
    '''\t"companion-server/internal/domain"\n\t"companion-server/internal/pipeline"''',
    '''\t"companion-server/internal/domain"\n\t"companion-server/internal/observability"\n\t"companion-server/internal/pipeline"''',
)
replace_once(
    "backend/internal/server/server.go",
    '''\tprivacy           *privacy.Service\n\tfeatureCatalog    *controlplane.FeatureCatalog\n}''',
    '''\tprivacy           *privacy.Service\n\tfeatureCatalog    *controlplane.FeatureCatalog\n\tobserver          observability.Recorder\n}''',
)
replace_once(
    "backend/internal/server/server.go",
    '''func WithDeviceAuthenticator(a DeviceAuthenticator) Option {\n\treturn func(s *Server) { s.deviceAuth = a }\n}\n\nfunc WithLocation''',
    '''func WithDeviceAuthenticator(a DeviceAuthenticator) Option {\n\treturn func(s *Server) { s.deviceAuth = a }\n}\n\nfunc WithObservabilityRecorder(recorder observability.Recorder) Option {\n\treturn func(s *Server) {\n\t\tif recorder != nil {\n\t\t\ts.observer = recorder\n\t\t}\n\t}\n}\n\nfunc WithLocation''',
)
replace_once(
    "backend/internal/server/server.go",
    '''\tservice := &Server{\n\t\tcomponents: components, logger: logger,\n\t\thub: newSessionHub(), schedulerInterval: 2 * time.Second, location: time.Local, identityResolver: HeaderIdentityResolver{DefaultUserID: "default"},\n\t}''',
    '''\tservice := &Server{\n\t\tcomponents: components, logger: logger, observer: observability.Nop(),\n\t\thub: newSessionHub(), schedulerInterval: 2 * time.Second, location: time.Local, identityResolver: HeaderIdentityResolver{DefaultUserID: "default"},\n\t}''',
)
replace_once(
    "backend/internal/server/server.go",
    '''\tsession, err := newSession(connection, s.components, s.hub, identity.DeviceID, identity.UserID, identity.ThreadID, identity.TenantID, identity.Plan, ack, s.controlPlane, s.logger)''',
    '''\tsession, err := newSession(connection, s.components, s.hub, identity.DeviceID, identity.UserID, identity.ThreadID, identity.TenantID, identity.Plan, ack, s.controlPlane, s.observer, s.logger)''',
)
replace_once(
    "backend/internal/server/server.go",
    '''\tif err := session.run(request.Context()); err != nil && !errors.Is(err, context.Canceled) {\n\t\ts.logger.Info("device session ended", "session_id", session.id, "error", err)\n\t}\n}''',
    '''\tsessionStarted := time.Now()\n\tsessionErr := session.run(request.Context())\n\tobservability.RecordTo(s.observer, observability.Event{\n\t\tName: observability.EventSessionEnd, DurationMS: time.Since(sessionStarted).Milliseconds(),\n\t\tOutcome: observabilityOutcome(sessionErr), Correlation: observability.Correlation{SessionID: session.id},\n\t})\n\tif sessionErr != nil && !errors.Is(sessionErr, context.Canceled) {\n\t\ts.logger.Info("device session ended", "session_id", session.id, "error", sessionErr)\n\t}\n}''',
)
replace_once(
    "backend/internal/server/server.go",
    '''\tlogger        *slog.Logger\n\tcontrolWrites chan outbound''',
    '''\tlogger        *slog.Logger\n\tobserver      observability.Recorder\n\tcontrolWrites chan outbound''',
)
replace_once(
    "backend/internal/server/server.go",
    '''func newSession(connection *websocket.Conn, components pipeline.Components, hub *sessionHub, deviceID, userID, threadID, tenantID, plan string, ack func(context.Context, int64) error, control *controlplane.Service, logger *slog.Logger) (*session, error) {''',
    '''func newSession(connection *websocket.Conn, components pipeline.Components, hub *sessionHub, deviceID, userID, threadID, tenantID, plan string, ack func(context.Context, int64) error, control *controlplane.Service, observer observability.Recorder, logger *slog.Logger) (*session, error) {''',
)
replace_once(
    "backend/internal/server/server.go",
    '''\tif strings.TrimSpace(threadID) == "" {\n\t\tthreadID = deviceID\n\t\tif strings.TrimSpace(threadID) == "" {\n\t\t\tthreadID = "default"\n\t\t}\n\t}\n\treturn &session{''',
    '''\tif strings.TrimSpace(threadID) == "" {\n\t\tthreadID = deviceID\n\t\tif strings.TrimSpace(threadID) == "" {\n\t\t\tthreadID = "default"\n\t\t}\n\t}\n\tif observer == nil {\n\t\tobserver = observability.Nop()\n\t}\n\treturn &session{''',
)
replace_once(
    "backend/internal/server/server.go",
    '''\t\tlogger:        logger,\n\t\tcontrolWrites: make(chan outbound, maximumControlQueue),''',
    '''\t\tlogger:        logger,\n\t\tobserver:      observer,\n\t\tcontrolWrites: make(chan outbound, maximumControlQueue),''',
)
replace_once(
    "backend/internal/server/server.go",
    '''\tif err := s.sendJSONMeta(ctx, protocol.SessionReadyType, protocol.Metadata{CorrelationID: hello.MessageID}, response); err != nil {\n\t\treturn err\n\t}\n\tif s.hub != nil {''',
    '''\tif err := s.sendJSONMeta(ctx, protocol.SessionReadyType, protocol.Metadata{CorrelationID: hello.MessageID}, response); err != nil {\n\t\treturn err\n\t}\n\tobservability.RecordTo(s.observer, observability.Event{\n\t\tName: observability.EventSessionReady, Outcome: "ok",\n\t\tCorrelation: observability.Correlation{SessionID: s.id},\n\t})\n\tif s.hub != nil {''',
)
replace_once(
    "backend/internal/server/server.go",
    '''\ts.mu.Unlock()\n\n\tif interrupted != nil {\n\t\t// This terminal notification''',
    '''\ts.mu.Unlock()\n\n\tobservability.RecordTo(s.observer, observability.Event{\n\t\tName: observability.EventTurnStart, Outcome: "ok",\n\t\tCorrelation: observability.Correlation{SessionID: s.id, TurnID: turnID, GenerationID: generation},\n\t})\n\tif interrupted != nil {\n\t\tobservability.RecordTo(s.observer, observability.Event{\n\t\t\tName: observability.EventTurnInterrupted, Outcome: "cancelled", Reason: "barge_in",\n\t\t\tCorrelation: observability.Correlation{SessionID: s.id, TurnID: interrupted.id, GenerationID: interrupted.generation},\n\t\t})\n\t\t// This terminal notification''',
)
replace_once(
    "backend/internal/server/server.go",
    '''func (s *session) processTurn(current *turn, pcm []byte) {\n\tturnStarted := time.Now()''',
    '''func (s *session) turnCorrelation(current *turn) observability.Correlation {\n\tif current == nil {\n\t\treturn observability.Correlation{SessionID: s.id}\n\t}\n\treturn observability.Correlation{SessionID: s.id, TurnID: current.id, GenerationID: current.generation}\n}\n\nfunc observabilityOutcome(err error) string {\n\tif err == nil {\n\t\treturn "ok"\n\t}\n\tif errors.Is(err, context.Canceled) {\n\t\treturn "cancelled"\n\t}\n\treturn "error"\n}\n\nfunc (s *session) processTurn(current *turn, pcm []byte) {\n\tturnStarted := time.Now()''',
)
replace_once(
    "backend/internal/server/server.go",
    '''\truntimeCtx := pipeline.WithTurnContext(current.ctx, runtimeTurn)\n\tasrStarted := turnStarted\n\ttranscript, err := s.components.ASR.Transcribe(runtimeCtx, pcm)\n\tasrDuration := time.Since(asrStarted)''',
    '''\truntimeCtx := observability.WithRecorder(current.ctx, s.observer)\n\truntimeCtx = observability.WithCorrelation(runtimeCtx, s.turnCorrelation(current))\n\truntimeCtx = pipeline.WithTurnContext(runtimeCtx, runtimeTurn)\n\tasrStarted := turnStarted\n\ttranscript, err := s.components.ASR.Transcribe(runtimeCtx, pcm)\n\tasrDuration := time.Since(asrStarted)\n\tobservability.Record(runtimeCtx, observability.Event{Name: observability.EventTurnStage, Stage: "asr", DurationMS: asrDuration.Milliseconds(), Outcome: observabilityOutcome(err)})''',
)
replace_once(
    "backend/internal/server/server.go",
    '''\tagentCtx := pipeline.WithTurnContext(current.ctx, runtimeTurn)''',
    '''\tagentCtx := pipeline.WithTurnContext(runtimeCtx, runtimeTurn)''',
)
replace_once(
    "backend/internal/server/server.go",
    '''\t\tmetrics, streamErr := s.processStreamingReply(current, agentCtx, transcript, streaming)\n\t\tif streamErr != nil {''',
    '''\t\tmetrics, streamErr := s.processStreamingReply(current, agentCtx, transcript, streaming)\n\t\tobservability.Record(agentCtx, observability.Event{Name: observability.EventTurnStage, Stage: "agent_stream", DurationMS: metrics.AgentTotal.Milliseconds(), Outcome: observabilityOutcome(streamErr)})\n\t\tif metrics.FirstSegmentAt > 0 {\n\t\t\tobservability.Record(agentCtx, observability.Event{Name: observability.EventTurnStage, Stage: "first_segment", DurationMS: metrics.FirstSegmentAt.Milliseconds(), Outcome: "ok"})\n\t\t}\n\t\tif metrics.TTSActive > 0 {\n\t\t\tobservability.Record(agentCtx, observability.Event{Name: observability.EventTurnStage, Stage: "tts_stream", DurationMS: metrics.TTSActive.Milliseconds(), Outcome: observabilityOutcome(streamErr)})\n\t\t}\n\t\tif streamErr != nil {''',
)
replace_once(
    "backend/internal/server/server.go",
    '''\t\ts.logger.Info("streaming voice turn completed",\n\t\t\t"turn_id", current.id, "session_id", s.id, "device_id", s.deviceID, "user_id", s.userID,''',
    '''\t\tobservability.Record(agentCtx, observability.Event{Name: observability.EventTurnEnd, DurationMS: time.Since(turnStarted).Milliseconds(), Outcome: "ok"})\n\t\ts.logger.Info("streaming voice turn completed",\n\t\t\t"turn_id", current.id, "session_id", s.id, "device_id", s.deviceID, "user_id", s.userID,''',
)
replace_once(
    "backend/internal/server/server.go",
    '''\tagentDuration := time.Since(agentStarted)\n\tif err != nil {''',
    '''\tagentDuration := time.Since(agentStarted)\n\tobservability.Record(agentCtx, observability.Event{Name: observability.EventTurnStage, Stage: "agent", DurationMS: agentDuration.Milliseconds(), Outcome: observabilityOutcome(err)})\n\tif err != nil {''',
)
replace_once(
    "backend/internal/server/server.go",
    '''\tttsStarted := time.Now()\n\terr = s.components.TTS.Synthesize(agentCtx, reply, func(frame []byte) error {\n\t\tpacket, err := s.codec.EncodeDownlink(frame)\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n\t\treturn s.sendTurn(current.ctx, current, outbound{kind: websocket.MessageBinary, data: packet})\n\t})\n\tif err != nil {''',
    '''\tttsStarted := time.Now()\n\terr = s.components.TTS.Synthesize(agentCtx, reply, func(frame []byte) error {\n\t\tpacket, err := s.codec.EncodeDownlink(frame)\n\t\tif err != nil {\n\t\t\treturn err\n\t\t}\n\t\treturn s.sendTurn(current.ctx, current, outbound{kind: websocket.MessageBinary, data: packet})\n\t})\n\tttsDuration := time.Since(ttsStarted)\n\tobservability.Record(agentCtx, observability.Event{Name: observability.EventTurnStage, Stage: "tts", DurationMS: ttsDuration.Milliseconds(), Outcome: observabilityOutcome(err)})\n\tif err != nil {''',
)
replace_once(
    "backend/internal/server/server.go",
    '''\tttsDuration := time.Since(ttsStarted)\n\ts.logger.Info("voice turn completed",''',
    '''\tobservability.Record(agentCtx, observability.Event{Name: observability.EventTurnEnd, DurationMS: time.Since(turnStarted).Milliseconds(), Outcome: "ok"})\n\ts.logger.Info("voice turn completed",''',
)
replace_once(
    "backend/internal/server/server.go",
    '''\tif current != nil {\n\t\t_ = s.sendJSONMeta(ctx, protocol.TurnStateType, protocol.Metadata{\n\t\t\tTurnID: current.id, GenerationID: current.generation,\n\t\t}, protocol.TurnStatePayload{State: "interrupted", Reason: reason})\n\t}\n}''',
    '''\tif current != nil {\n\t\tobservability.RecordTo(s.observer, observability.Event{\n\t\t\tName: observability.EventTurnInterrupted, Outcome: "cancelled", Reason: reason, Correlation: s.turnCorrelation(current),\n\t\t})\n\t\t_ = s.sendJSONMeta(ctx, protocol.TurnStateType, protocol.Metadata{\n\t\t\tTurnID: current.id, GenerationID: current.generation,\n\t\t}, protocol.TurnStatePayload{State: "interrupted", Reason: reason})\n\t}\n}''',
)
replace_once(
    "backend/internal/server/server.go",
    '''func (s *session) failTurn(current *turn, code string, err error) {\n\tif !s.isCurrent(current) {\n\t\treturn\n\t}\n\ts.sendTurnError(current.ctx, current, code, err.Error())''',
    '''func (s *session) failTurn(current *turn, code string, err error) {\n\tif !s.isCurrent(current) {\n\t\treturn\n\t}\n\tobservability.RecordTo(s.observer, observability.Event{Name: observability.EventTurnEnd, Outcome: "error", Reason: code, Correlation: s.turnCorrelation(current)})\n\ts.sendTurnError(current.ctx, current, code, err.Error())''',
)
replace_once(
    "backend/internal/server/server.go",
    '''\tdefault:\n\t\treturn fmt.Errorf("client %s write queue is full", label)\n\t}\n}''',
    '''\tdefault:\n\t\tobservability.RecordTo(s.observer, observability.Event{\n\t\t\tName: observability.EventQueueFull, Outcome: "error", Queue: label, QueueCapacity: cap(queue),\n\t\t\tCorrelation: observability.Correlation{SessionID: s.id, GenerationID: message.generation},\n\t\t})\n\t\treturn fmt.Errorf("client %s write queue is full", label)\n\t}\n}''',
)

# Composition root: opt-in bounded snapshot for development/CI. No configured
# path means NOP; serialization occurs only during graceful shutdown.
replace_once(
    "backend/cmd/companiond/main.go",
    '''\t"net/http"\n\t"os"\n\t"os/signal"''',
    '''\t"net/http"\n\t"os"\n\t"os/signal"\n\t"path/filepath"''',
)
replace_once(
    "backend/cmd/companiond/main.go",
    '''\t"companion-server/internal/memory"\n\t"companion-server/internal/pipeline"''',
    '''\t"companion-server/internal/memory"\n\t"companion-server/internal/observability"\n\t"companion-server/internal/pipeline"''',
)
replace_once(
    "backend/cmd/companiond/main.go",
    '''func main() {\n\tlogger := slog.New(slog.NewJSONHandler(os.Stdout, nil))\n\truntimeCfg, err := runtimeconfig.Load()''',
    '''func main() {\n\tlogger := slog.New(slog.NewJSONHandler(os.Stdout, nil))\n\tobserver, flushObservability := configureObservability(logger)\n\tdefer flushObservability()\n\truntimeCfg, err := runtimeconfig.Load()''',
)
replace_once(
    "backend/cmd/companiond/main.go",
    '''\t\tserver.WithDeviceAuthenticator(data),\n\t}''',
    '''\t\tserver.WithDeviceAuthenticator(data),\n\t\tserver.WithObservabilityRecorder(observer),\n\t}''',
)
replace_once(
    "backend/cmd/companiond/main.go",
    '''func loadPromptBundle(cfg runtimeconfig.Config) (*promptpkg.Bundle, error) {''',
    '''func configureObservability(logger *slog.Logger) (observability.Recorder, func()) {\n\tpath := strings.TrimSpace(os.Getenv("COMPANION_OBSERVABILITY_FILE"))\n\tif path == "" {\n\t\treturn observability.Nop(), func() {}\n\t}\n\tcapacity := 4096\n\tif raw := strings.TrimSpace(os.Getenv("COMPANION_OBSERVABILITY_CAPACITY")); raw != "" {\n\t\tif parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {\n\t\t\tcapacity = parsed\n\t\t}\n\t}\n\trecorder := observability.NewRingRecorder(capacity)\n\tflush := func() {\n\t\tsnapshot := recorder.Snapshot()\n\t\tif err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {\n\t\t\tlogger.Error("create observability snapshot directory", "error", err)\n\t\t\treturn\n\t\t}\n\t\tfile, err := os.Create(path)\n\t\tif err != nil {\n\t\t\tlogger.Error("create observability snapshot", "error", err)\n\t\t\treturn\n\t\t}\n\t\tdefer file.Close()\n\t\tif err := observability.WriteSnapshot(file, snapshot); err != nil {\n\t\t\tlogger.Error("write observability snapshot", "error", err)\n\t\t\treturn\n\t\t}\n\t\tlogger.Info("observability snapshot written", "events", len(snapshot.Events), "dropped", snapshot.Dropped)\n\t}\n\treturn recorder, flush\n}\n\nfunc loadPromptBundle(cfg runtimeconfig.Config) (*promptpkg.Bundle, error) {''',
)

# Tier-1 exports/validates product instrumentation snapshots.
replace_once(
    "host/companion_software_device/run_e2e.sh",
    '''TOOL_DB_OUT="${OUT%.json}-tool-db.json"\nTMP="$(mktemp -d)"''',
    '''TOOL_DB_OUT="${OUT%.json}-tool-db.json"\nCORE_OBS_OUT="${OUT%.json}-observability-core.json"\nTMP="$(mktemp -d)"''',
)
replace_once(
    "host/companion_software_device/run_e2e.sh",
    '''COMPANION_EVIDENCE_CONFIG_SHA256="$(printf '%s\\n' \\\n  'profile=test' 'allow_mock=true' 'agent=adk:fake_responses' \\\n  'auth=database_enrolled' 'asr=mock:tier1 transcript' \\\n  'tts=mock' 'protocol=v2' | sha256sum | awk '{print $1}')"\nstart_server "$SERVER_LOG"''',
    '''COMPANION_EVIDENCE_CONFIG_SHA256="$(printf '%s\\n' \\\n  'profile=test' 'allow_mock=true' 'agent=adk:fake_responses' \\\n  'auth=database_enrolled' 'asr=mock:tier1 transcript' \\\n  'tts=mock' 'protocol=v2' | sha256sum | awk '{print $1}')"\nexport COMPANION_OBSERVABILITY_FILE="$CORE_OBS_OUT"\nstart_server "$SERVER_LOG"''',
)
replace_once(
    "host/companion_software_device/run_e2e.sh",
    '''python3 "$ROOT/host/companion_software_device/validate_evidence.py" "$OUT"\nstop_server\n\n# Representative ADK tool parity.''',
    '''python3 "$ROOT/host/companion_software_device/validate_evidence.py" "$OUT"\nstop_server\npython3 "$ROOT/host/companion_software_device/validate_observability.py" "$CORE_OBS_OUT" core\n\n# Representative ADK tool parity.''',
)
replace_once(
    "host/companion_software_device/run_e2e.sh",
    '''TOOL_CASES=(\n  "expense|Tier1 expense 50k|$TOOL_OUT"\n  "budget|Tier1 budget weekly|${OUT%.json}-tool-budget.json"\n  "note|Tier1 note|${OUT%.json}-tool-note.json"\n  "journal|Tier1 journal|${OUT%.json}-tool-journal.json"\n  "reminder|Tier1 reminder|${OUT%.json}-tool-reminder.json"\n  "timer|Tier1 timer|${OUT%.json}-tool-timer.json"\n  "memory|Tier1 memory|${OUT%.json}-tool-memory.json"\n)\n\nfor spec in "${TOOL_CASES[@]}"; do\n  IFS='|' read -r case_id transcript evidence_path <<<"$spec"''',
    '''TOOL_CASES=(\n  "expense|Tier1 expense 50k|$TOOL_OUT|expense.log"\n  "budget|Tier1 budget weekly|${OUT%.json}-tool-budget.json|budget.set"\n  "note|Tier1 note|${OUT%.json}-tool-note.json|note.create"\n  "journal|Tier1 journal|${OUT%.json}-tool-journal.json|journal.create"\n  "reminder|Tier1 reminder|${OUT%.json}-tool-reminder.json|reminder.create"\n  "timer|Tier1 timer|${OUT%.json}-tool-timer.json|timer.create"\n  "memory|Tier1 memory|${OUT%.json}-tool-memory.json|memory.remember"\n)\n\nfor spec in "${TOOL_CASES[@]}"; do\n  IFS='|' read -r case_id transcript evidence_path expected_tool <<<"$spec"''',
)
replace_once(
    "host/companion_software_device/run_e2e.sh",
    '''  COMPANION_EVIDENCE_CONFIG_SHA256="$(printf '%s\\n' \\\n    'profile=test' 'allow_mock=true' 'agent=adk:fake_responses' \\\n    'auth=database_enrolled' "asr=mock:${transcript}" \\\n    'tts=mock' 'protocol=v2' "tool_case=${case_id}" | sha256sum | awk '{print $1}')"\n  start_server "$TOOL_SERVER_LOG"''',
    '''  COMPANION_EVIDENCE_CONFIG_SHA256="$(printf '%s\\n' \\\n    'profile=test' 'allow_mock=true' 'agent=adk:fake_responses' \\\n    'auth=database_enrolled' "asr=mock:${transcript}" \\\n    'tts=mock' 'protocol=v2' "tool_case=${case_id}" | sha256sum | awk '{print $1}')"\n  export COMPANION_OBSERVABILITY_FILE="${evidence_path%.json}-observability.json"\n  start_server "$TOOL_SERVER_LOG"''',
)
replace_once(
    "host/companion_software_device/run_e2e.sh",
    '''  python3 "$ROOT/host/companion_software_device/validate_evidence.py" "$evidence_path"\n  stop_server\ndone''',
    '''  python3 "$ROOT/host/companion_software_device/validate_evidence.py" "$evidence_path"\n  stop_server\n  python3 "$ROOT/host/companion_software_device/validate_observability.py" "${evidence_path%.json}-observability.json" tool "$expected_tool"\ndone''',
)
replace_once(
    "host/companion_software_device/run_e2e.sh",
    '''export MOCK_TRANSCRIPT="Tier1 memory"\nstart_server "$TOOL_SERVER_LOG"''',
    '''export MOCK_TRANSCRIPT="Tier1 memory"\nexport COMPANION_OBSERVABILITY_FILE="$TMP/final-auth-observability.json"\nstart_server "$TOOL_SERVER_LOG"''',
)

# README truth refresh for the already-merged Wave-0 state and new contract.
replace_once(
    "README.md",
    '''> **Active baseline:** the production evidence platform from merged PR #1; physical/provider gates remain unproven''',
    '''> **Active baseline:** `main` includes protocol v2, Tier-1 evidence, repository governance and the ADK/auth single-path hard cut through merged PR #34; physical/provider gates remain unproven''',
)
replace_once(
    "README.md",
    '''- **Native MCP bridge** — official MCP Go SDK behind the Companion `ToolRegistry`/policy boundary, with endpoint validation and SSRF-safe defaults.''',
    '''- **MCP adapter foundation** — official MCP Go SDK helper code exists behind the Companion `ToolRegistry`/policy boundary; product startup wiring and real external interoperability remain owned by #19 and are not claimed yet.''',
)
replace_once(
    "README.md",
    '''- **Tier-1 headless software device** — production C++ `CompanionApp` + protocol v2 connect to real `companiond` through a host-only WebSocket/libopus adapter; the harness covers reconnect/barge-in/replay/config, wrong/revoked device credentials, ADK tool loops, and representative authoritative mutations for expense/budget/note/journal/reminder/timer/memory. Deterministic providers remain `orchestration_only`.''',
    '''- **Tier-1 headless software device** — production C++ `CompanionApp` + protocol v2 connect to real `companiond` through a host-only WebSocket/libopus adapter; the harness covers reconnect/barge-in/replay/config, wrong/revoked device credentials, ADK tool loops, and representative authoritative mutations for expense/budget/note/journal/reminder/timer/memory. Deterministic providers remain `orchestration_only`.\n- **Observability contract (#25 in progress)** — Companion-owned bounded/non-blocking event schema with safe session/turn/generation correlation, tool outcome timing and Tier-1 JSON snapshots; no hosted telemetry vendor is selected by the runtime contract.''',
)
replace_once(
    "README.md",
    '''  ├─ current SQLite compatibility store''',
    '''  ├─ current authoritative SQLite store''',
)
replace_once(
    "README.md",
    '''- [`docs/TEST_EVIDENCE_LADDER.md`](docs/TEST_EVIDENCE_LADDER.md) — Tier 0/1/2/3 evidence classes, promotion limits and current software-device/Wokwi boundaries.''',
    '''- [`docs/TEST_EVIDENCE_LADDER.md`](docs/TEST_EVIDENCE_LADDER.md) — Tier 0/1/2/3 evidence classes, promotion limits and current software-device/Wokwi boundaries.\n- [`docs/OBSERVABILITY.md`](docs/OBSERVABILITY.md) — metric/event naming, correlation, cardinality/privacy and exporter contract.''',
)
replace_once(
    "README.md",
    '''The active PR is intentionally **not** a new production checkpoint yet. It remains draft until its scope is reviewed, synced with `main`, CI reruns on the merged tree, independent static review is recorded, and every production claim has matching evidence.''',
    '''`main` is currently ahead of the last immutable software tag after the verified Wave-0 merges. A new production checkpoint is created only after the next intended scope is reviewed, exact-head CI and post-merge verification pass, independent static review is recorded, and every promoted production claim has matching evidence.''',
)

print("issue25 guarded patch applied")
