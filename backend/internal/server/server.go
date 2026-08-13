package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"companion-server/internal/capability"
	"companion-server/internal/controlplane"
	"companion-server/internal/domain"
	"companion-server/internal/observability"
	"companion-server/internal/pipeline"
	"companion-server/internal/privacy"
	"companion-server/internal/protocol"

	"github.com/coder/websocket"
)

const (
	maximumControlQueue     = 32
	maximumMediaQueue       = 24
	helloTimeout            = 10 * time.Second
	sessionLoopJoinTimeout  = time.Second
	turnCancellationJoinMax = 250 * time.Millisecond
)

type Server struct {
	components        pipeline.Components
	logger            *slog.Logger
	hub               *sessionHub
	data              SchedulerRepository
	schedulerInterval time.Duration
	location          *time.Location
	identityResolver  IdentityResolver
	controlPlane      *controlplane.Service
	adminToken        string
	deviceAuth        DeviceAuthenticator
	credentials       DeviceCredentialManager
	entitlements      EntitlementManager
	firmware          *controlplane.FirmwareService
	privacy           *privacy.Service
	featureCatalog    *controlplane.FeatureCatalog
	observer          observability.Recorder
}

type Option func(*Server)

func WithStore(data SchedulerRepository) Option {
	return func(s *Server) { s.data = data }
}

func WithSchedulerInterval(interval time.Duration) Option {
	return func(s *Server) { s.schedulerInterval = interval }
}

func WithIdentityResolver(resolver IdentityResolver) Option {
	return func(s *Server) {
		if resolver != nil {
			s.identityResolver = resolver
		}
	}
}

func WithControlPlane(c *controlplane.Service) Option { return func(s *Server) { s.controlPlane = c } }
func WithFirmwareService(f *controlplane.FirmwareService) Option {
	return func(s *Server) { s.firmware = f }
}
func WithPrivacyService(p *privacy.Service) Option { return func(s *Server) { s.privacy = p } }
func WithFeatureCatalog(c *controlplane.FeatureCatalog) Option {
	return func(s *Server) { s.featureCatalog = c }
}
func WithAdminToken(token string) Option {
	return func(s *Server) { s.adminToken = strings.TrimSpace(token) }
}

type DeviceAuthenticator interface {
	AuthenticateDevice(context.Context, string, string) (domain.Identity, bool, error)
}
type DeviceCredentialManager interface {
	EnrollDevice(context.Context, domain.Identity, string) error
	RevokeDevice(context.Context, string) error
}
type EntitlementManager interface {
	SetEntitlement(context.Context, string, string, bool, *time.Time) error
}

func WithEntitlementManager(m EntitlementManager) Option {
	return func(s *Server) { s.entitlements = m }
}

func WithDeviceCredentialManager(m DeviceCredentialManager) Option {
	return func(s *Server) { s.credentials = m }
}

func WithDeviceAuthenticator(a DeviceAuthenticator) Option {
	return func(s *Server) { s.deviceAuth = a }
}

func WithObservabilityRecorder(recorder observability.Recorder) Option {
	return func(s *Server) {
		if recorder != nil {
			s.observer = recorder
		}
	}
}

func WithLocation(location *time.Location) Option {
	return func(s *Server) {
		if location != nil {
			s.location = location
		}
	}
}

func New(components pipeline.Components, logger *slog.Logger, options ...Option) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	service := &Server{
		components: components, logger: logger, observer: observability.Nop(),
		hub: newSessionHub(), schedulerInterval: 2 * time.Second, location: time.Local, identityResolver: HeaderIdentityResolver{DefaultUserID: "default"},
	}
	for _, option := range options {
		option(service)
	}
	return service
}

// RunBackground runs durable/proactive workers until ctx is cancelled.
// It is intentionally separate from Handler so unit tests can control lifecycle.
func (s *Server) RunBackground(ctx context.Context) {
	if s.data == nil {
		return
	}
	newReminderScheduler(s.data, s.hub, s.schedulerInterval, s.location, s.logger).run(ctx)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
		writer.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /v2/device", s.handleDevice)
	if s.firmware != nil {
		mux.HandleFunc("GET /v1/ota", s.handleOTAGet)
	}
	if s.controlPlane != nil && s.adminToken != "" {
		mux.HandleFunc("GET /v1/admin/devices/{deviceID}/twin", s.handleTwinGet)
		mux.HandleFunc("PATCH /v1/admin/devices/{deviceID}/config", s.handleTwinPatch)
		mux.HandleFunc("GET /v1/admin/config-schema", s.handleConfigSchema)
		mux.HandleFunc("GET /v1/admin/config/{scopeType}/{scopeID}", s.handleScopedConfigGet)
		mux.HandleFunc("PATCH /v1/admin/config/{scopeType}/{scopeID}", s.handleScopedConfigPatch)
		mux.HandleFunc("GET /v1/admin/features", s.handleFeaturesGet)
		mux.HandleFunc("PATCH /v1/admin/features/{key}", s.handleFeaturePatch)
	}
	if s.credentials != nil && s.adminToken != "" {
		mux.HandleFunc("POST /v1/admin/devices/{deviceID}/credential", s.handleCredentialEnroll)
		mux.HandleFunc("DELETE /v1/admin/devices/{deviceID}/credential", s.handleCredentialRevoke)
	}
	if s.entitlements != nil && s.adminToken != "" {
		mux.HandleFunc("PATCH /v1/admin/users/{userID}/entitlements/{key}", s.handleEntitlementPatch)
	}
	if s.firmware != nil && s.adminToken != "" {
		mux.HandleFunc("POST /v1/admin/firmware", s.handleFirmwarePublish)
	}
	if s.privacy != nil && s.adminToken != "" {
		mux.HandleFunc("GET /v1/admin/users/{userID}/privacy", s.handlePrivacyGet)
		mux.HandleFunc("PATCH /v1/admin/users/{userID}/privacy", s.handlePrivacyPatch)
	}
	if s.featureCatalog != nil && s.adminToken != "" {
		mux.HandleFunc("GET /v1/admin/modules", s.handleModulesGet)
		mux.HandleFunc("PUT /v1/admin/modules/{id}", s.handleModulePut)
	}
	return mux
}

func (s *Server) authenticateDeviceRequest(ctx context.Context, request *http.Request) (domain.Identity, bool) {
	deviceID := strings.TrimSpace(request.Header.Get("Device-Id"))
	if s.deviceAuth == nil {
		return domain.Identity{DeviceID: deviceID}, false
	}
	raw := strings.TrimSpace(strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer "))
	if deviceID == "" || raw == "" {
		return domain.Identity{DeviceID: deviceID}, false
	}
	identity, ok, err := s.deviceAuth.AuthenticateDevice(ctx, deviceID, raw)
	return identity, err == nil && ok
}

func (s *Server) handleDevice(writer http.ResponseWriter, request *http.Request) {
	authenticated, ok := s.authenticateDeviceRequest(request.Context(), request)
	if !ok {
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
		return
	}
	connection, err := websocket.Accept(writer, request, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		s.logger.Warn("websocket accept failed", "error", err)
		return
	}
	connection.SetReadLimit(protocol.MaximumEnvelopeBytes)
	identity := s.identityResolver.Resolve(request, authenticated.DeviceID)
	if s.deviceAuth != nil {
		// In database-auth mode ownership/tenant/plan are trusted enrollment claims,
		// never client-controlled transport headers. Thread remains a conversation concern.
		identity.UserID = authenticated.UserID
		identity.TenantID = authenticated.TenantID
		identity.Plan = authenticated.Plan
		identity.DeviceID = authenticated.DeviceID
	}
	ack := func(ctx context.Context, id int64) error {
		if s.data == nil {
			return fmt.Errorf("scheduler repository unavailable")
		}
		return s.data.AcknowledgeReminder(ctx, identity.UserID, identity.DeviceID, id)
	}
	session, err := newSession(connection, s.components, s.hub, identity.DeviceID, identity.UserID, identity.ThreadID, identity.TenantID, identity.Plan, ack, s.controlPlane, s.observer, s.logger)
	if err != nil {
		connection.Close(websocket.StatusInternalError, "codec unavailable")
		s.logger.Error("initialize Opus session", "error", err)
		return
	}
	sessionStarted := time.Now()
	sessionErr := session.run(request.Context())
	observability.RecordTo(s.observer, observability.Event{
		Name: observability.EventSessionEnd, DurationMS: time.Since(sessionStarted).Milliseconds(),
		Outcome: observabilityOutcome(sessionErr), Correlation: observability.Correlation{SessionID: session.id},
	})
	if sessionErr != nil && !errors.Is(sessionErr, context.Canceled) {
		s.logger.Info("device session ended", "session_id", session.id, "error", sessionErr)
	}
}

type outbound struct {
	kind       websocket.MessageType
	data       []byte
	generation uint64
	turnScoped bool
}

type turn struct {
	id         string
	state      string
	pcm        []byte
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	generation uint64
}

type session struct {
	id            string
	deviceID      string
	userID        string
	threadID      string
	tenantID      string
	plan          string
	ackReminder   func(context.Context, int64) error
	connection    *websocket.Conn
	components    pipeline.Components
	hub           *sessionHub
	logger        *slog.Logger
	observer      observability.Recorder
	controlWrites chan outbound
	mediaWrites   chan outbound
	mu            sync.Mutex
	active        *turn
	generation    uint64
	codec         pipeline.AudioCodec
	controlPlane  *controlplane.Service
	messageSeq    atomic.Uint64
	seenInbound   map[string]inboundRecord
	seenOrder     []string
}

type inboundRecord struct {
	digest  [sha256.Size]byte
	outcome error
}

func newSession(connection *websocket.Conn, components pipeline.Components, hub *sessionHub, deviceID, userID, threadID, tenantID, plan string, ack func(context.Context, int64) error, control *controlplane.Service, observer observability.Recorder, logger *slog.Logger) (*session, error) {
	if components.Codecs == nil {
		return nil, fmt.Errorf("codec factory is required")
	}
	codec, err := components.Codecs.New()
	if err != nil {
		return nil, err
	}
	id := randomID()
	if strings.TrimSpace(deviceID) == "" {
		deviceID = id
	}
	if strings.TrimSpace(userID) == "" {
		userID = "default"
	}
	if strings.TrimSpace(threadID) == "" {
		threadID = deviceID
		if strings.TrimSpace(threadID) == "" {
			threadID = "default"
		}
	}
	if observer == nil {
		observer = observability.Nop()
	}
	return &session{
		id:            id,
		deviceID:      deviceID,
		userID:        userID,
		threadID:      threadID,
		tenantID:      strings.TrimSpace(tenantID),
		plan:          strings.TrimSpace(plan),
		ackReminder:   ack,
		connection:    connection,
		components:    components,
		hub:           hub,
		logger:        logger,
		observer:      observer,
		controlWrites: make(chan outbound, maximumControlQueue),
		mediaWrites:   make(chan outbound, maximumMediaQueue),
		codec:         codec,
		controlPlane:  control,
		seenInbound:   make(map[string]inboundRecord),
	}, nil
}

func (s *session) run(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	defer s.connection.CloseNow()

	loopExited := make(chan struct{}, 2)
	writerDone := make(chan error, 1)
	loopCount := 1
	go func() {
		defer func() { loopExited <- struct{}{} }()
		writerDone <- s.writeLoop(ctx)
	}()

	helloCtx, helloCancel := context.WithTimeout(ctx, helloTimeout)
	kind, data, err := s.connection.Read(helloCtx)
	helloCancel()
	if err != nil {
		return fmt.Errorf("read hello: %w", err)
	}
	if kind != websocket.MessageText {
		return fmt.Errorf("hello must be a text message")
	}
	hello, err := protocol.Decode(data)
	if err != nil {
		_ = s.writeHandshakeError(ctx, protocol.ErrorCode(err), err.Error(), "")
		return fmt.Errorf("decode hello: %w", err)
	}
	helloPayload, err := protocol.DecodePayload[protocol.HelloPayload](hello)
	if err != nil {
		_ = s.writeHandshakeError(ctx, protocol.ErrorCode(err), err.Error(), hello.MessageID)
		return fmt.Errorf("decode hello payload: %w", err)
	}
	if err := protocol.ValidateHello(hello, helloPayload); err != nil {
		_ = s.writeHandshakeError(ctx, protocol.InvalidEnvelopeCode, err.Error(), hello.MessageID)
		return err
	}
	audio := protocol.DownlinkAudioParams()
	response := protocol.ReadyPayload{Transport: protocol.Transport, AudioParams: audio}
	if s.controlPlane != nil {
		if twin, e := s.controlPlane.ManifestFor(ctx, controlplane.ResolutionContext{UserID: s.userID, DeviceID: s.deviceID, TenantID: s.tenantID, Plan: s.plan}); e == nil {
			c := protocolConfig(twin.Desired)
			response.Config = &c
			response.ConfigVersion = twin.DesiredVersion
		}
	}
	if err := s.sendJSONMeta(ctx, protocol.SessionReadyType, protocol.Metadata{CorrelationID: hello.MessageID}, response); err != nil {
		return err
	}
	observability.RecordTo(s.observer, observability.Event{
		Name: observability.EventSessionReady, Outcome: "ok",
		Correlation: observability.Correlation{SessionID: s.id},
	})
	if s.hub != nil {
		s.hub.register(s.deviceID, s)
		defer s.hub.unregister(s.deviceID, s)
	}

	readDone := make(chan error, 1)
	loopCount++
	go func() {
		defer func() { loopExited <- struct{}{} }()
		readDone <- s.readLoop(ctx)
	}()
	var runErr error
	select {
	case runErr = <-readDone:
	case runErr = <-writerDone:
	case <-ctx.Done():
		runErr = ctx.Err()
	}
	cancel()
	s.connection.CloseNow()
	if !s.cancelActiveAndJoin(turnCancellationJoinMax) {
		s.logger.Warn("active turn did not join before session cancellation bound", "session_id", s.id, "join_ms", turnCancellationJoinMax.Milliseconds())
	}
	joinTimer := time.NewTimer(sessionLoopJoinTimeout)
	defer joinTimer.Stop()
	for joined := 0; joined < loopCount; joined++ {
		select {
		case <-loopExited:
		case <-joinTimer.C:
			s.logger.Warn("session loop did not join before shutdown bound", "session_id", s.id, "joined", joined, "expected", loopCount)
			return runErr
		}
	}
	return runErr
}

func (s *session) readLoop(ctx context.Context) error {
	for {
		kind, data, err := s.connection.Read(ctx)
		if err != nil {
			return err
		}
		switch kind {
		case websocket.MessageText:
			if err := s.handleControl(ctx, data); err != nil {
				s.sendError(ctx, protocol.ErrorCode(err), err.Error(), "")
			}
		case websocket.MessageBinary:
			if err := s.handleAudio(data); err != nil {
				s.sendError(ctx, "invalid_audio", err.Error(), "")
			}
		}
	}
}

func (s *session) writeLoop(ctx context.Context) error {
	write := func(message outbound) error {
		// A cancelled/interrupting turn may already have media/control items queued.
		// Never let those stale generation items reach the device after barge-in.
		if !s.outboundCurrent(message) {
			return nil
		}
		writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := s.connection.Write(writeCtx, message.kind, message.data)
		cancel()
		return err
	}
	for {
		// Always drain control first when available so alarms/config/abort are not
		// trapped behind the ordered TTS media stream.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case message := <-s.controlWrites:
			if err := write(message); err != nil {
				return err
			}
			continue
		default:
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case message := <-s.controlWrites:
			if err := write(message); err != nil {
				return err
			}
		case message := <-s.mediaWrites:
			if err := write(message); err != nil {
				return err
			}
		}
	}
}

func (s *session) handleControl(ctx context.Context, data []byte) error {
	message, err := protocol.Decode(data)
	if err != nil {
		return fmt.Errorf("decode control message: %w", err)
	}
	if message.SessionID != s.id {
		return fmt.Errorf("session_id does not match")
	}
	switch message.Type {
	case protocol.TurnListenType:
		payload, err := protocol.DecodePayload[protocol.ListenPayload](message)
		if err != nil {
			return err
		}
		return s.processInbound(message.MessageID, data, func() error {
			switch payload.State {
			case "start":
				return s.startTurn(ctx, message.TurnID)
			case "stop":
				return s.stopTurn(message.TurnID)
			default:
				return fmt.Errorf("unsupported listen state %q", payload.State)
			}
		})
	case protocol.TurnAbortType:
		if _, err := protocol.DecodePayload[protocol.AbortPayload](message); err != nil {
			return err
		}
		return s.processInbound(message.MessageID, data, func() error {
			s.interruptActive(ctx, "client_abort")
			return nil
		})
	case protocol.AlarmAckType:
		if s.ackReminder == nil {
			return fmt.Errorf("alarm acknowledgement is unavailable")
		}
		payload, err := protocol.DecodePayload[protocol.AlarmAckPayload](message)
		if err != nil {
			return err
		}
		id, err := parseAlarmID(payload.AlarmID)
		if err != nil {
			return err
		}
		return s.processInbound(message.MessageID, data, func() error { return s.ackReminder(ctx, id) })
	case protocol.ConfigReportType:
		if s.controlPlane == nil {
			return fmt.Errorf("config reporting unavailable")
		}
		payload, err := protocol.DecodePayload[protocol.ConfigReportPayload](message)
		if err != nil {
			return err
		}
		return s.processInbound(message.MessageID, data, func() error {
			if !payload.Applied {
				s.logger.Warn("device rejected runtime config", "device_id", s.deviceID, "version", payload.ConfigVersion)
				return nil
			}
			return s.controlPlane.Report(ctx, s.userID, s.deviceID, payload.ConfigVersion, controlConfig(payload.Config))
		})
	case protocol.SessionPingType:
		if _, err := protocol.DecodePayload[protocol.EmptyPayload](message); err != nil {
			return err
		}
		return s.sendJSONMeta(ctx, protocol.SessionPongType, protocol.Metadata{CorrelationID: message.MessageID}, protocol.EmptyPayload{})
	default:
		return &protocol.ProtocolError{Code: protocol.InvalidEnvelopeCode, Detail: fmt.Sprintf("message type %q is invalid in this direction", message.Type)}
	}
}

func (s *session) startTurn(parent context.Context, turnID string) error {
	if strings.TrimSpace(turnID) == "" {
		return fmt.Errorf("turn_id is required")
	}

	var interrupted *turn
	s.mu.Lock()
	if s.active != nil {
		interrupted = s.active
		interrupted.cancel()
		s.active = nil
	}
	s.generation++
	generation := s.generation
	ctx, cancel := context.WithCancel(parent)
	s.active = &turn{
		id: turnID, state: "listening", ctx: ctx, cancel: cancel,
		pcm: make([]byte, 0, protocol.UplinkSampleRate*2), generation: generation,
	}
	s.mu.Unlock()

	observability.RecordTo(s.observer, observability.Event{
		Name: observability.EventTurnStart, Outcome: "ok",
		Correlation: observability.Correlation{SessionID: s.id, TurnID: turnID, GenerationID: generation},
	})
	if interrupted != nil {
		joinStarted := time.Now()
		joined := waitForTurn(interrupted, turnCancellationJoinMax)
		joinDuration := time.Since(joinStarted)
		observability.RecordTo(s.observer, observability.Event{
			Name: observability.EventTurnInterrupted, Outcome: "cancelled", Reason: "barge_in", DurationMS: joinDuration.Milliseconds(),
			Correlation: observability.Correlation{SessionID: s.id, TurnID: interrupted.id, GenerationID: interrupted.generation},
		})
		if !joined {
			s.logger.Warn("barge-in turn exceeded cancellation join bound", "session_id", s.id, "turn_id", interrupted.id, "join_ms", joinDuration.Milliseconds())
		}
		// This terminal notification is intentionally not generation-scoped: it
		// must survive invalidation of the old turn and tell the device to drop
		// any already-buffered playback immediately.
		_ = s.sendJSONMeta(parent, protocol.TurnStateType, protocol.Metadata{
			TurnID: interrupted.id, GenerationID: interrupted.generation,
		}, protocol.TurnStatePayload{State: "interrupted", Reason: "barge_in"})
	}
	return nil
}

func (s *session) handleAudio(data []byte) error {
	if len(data) == 0 || len(data) > protocol.MaximumOpusPacketBytes {
		return fmt.Errorf("Opus packet has invalid size %d", len(data))
	}
	pcm, err := s.codec.DecodeUplink(data)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil || s.active.state != "listening" {
		return fmt.Errorf("audio received outside a listening turn")
	}
	maximum := protocol.UplinkSampleRate * 2 * protocol.MaximumAudioSecs
	if len(s.active.pcm)+len(pcm) > maximum {
		return fmt.Errorf("turn exceeds %d seconds", protocol.MaximumAudioSecs)
	}
	s.active.pcm = append(s.active.pcm, pcm...)
	return nil
}

func (s *session) stopTurn(turnID string) error {
	s.mu.Lock()
	if s.active == nil || s.active.id != turnID || s.active.state != "listening" {
		s.mu.Unlock()
		return fmt.Errorf("no matching listening turn")
	}
	if len(s.active.pcm) == 0 {
		s.mu.Unlock()
		return fmt.Errorf("turn contains no audio")
	}
	s.active.state = "processing"
	current := s.active
	current.done = make(chan struct{})
	pcm := append([]byte(nil), current.pcm...)
	s.mu.Unlock()
	go func() {
		defer close(current.done)
		s.processTurn(current, pcm)
	}()
	return nil
}

func (s *session) turnCorrelation(current *turn) observability.Correlation {
	if current == nil {
		return observability.Correlation{SessionID: s.id}
	}
	return observability.Correlation{SessionID: s.id, TurnID: current.id, GenerationID: current.generation}
}

func observabilityOutcome(err error) string {
	if err == nil {
		return "ok"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	return "error"
}

func (s *session) processTurn(current *turn, pcm []byte) {
	turnStarted := time.Now()
	runtimeTurn := pipeline.TurnContext{UserID: s.userID, ThreadID: s.threadID, DeviceID: s.deviceID, TenantID: s.tenantID, Plan: s.plan, SessionID: s.id, TurnID: current.id}
	if s.controlPlane != nil {
		if twin, err := s.controlPlane.ManifestFor(current.ctx, controlplane.ResolutionContext{UserID: s.userID, DeviceID: s.deviceID, TenantID: s.tenantID, Plan: s.plan}); err == nil {
			runtimeTurn.Locale = twin.Desired.Locale
			runtimeTurn.Timezone = twin.Desired.Timezone
			runtimeTurn.VoiceKey = twin.Desired.VoiceKey
		}
	}
	runtimeCtx := observability.WithRecorder(current.ctx, s.observer)
	runtimeCtx = observability.WithCorrelation(runtimeCtx, s.turnCorrelation(current))
	runtimeCtx = pipeline.WithTurnContext(runtimeCtx, runtimeTurn)
	asrStarted := turnStarted
	transcript, err := s.components.ASR.Transcribe(runtimeCtx, pcm)
	asrDuration := time.Since(asrStarted)
	observability.Record(runtimeCtx, observability.Event{Name: observability.EventTurnStage, Stage: "asr", DurationMS: asrDuration.Milliseconds(), Outcome: observabilityOutcome(err)})
	if err != nil {
		s.failTurn(current, "asr_failed", err)
		return
	}
	if !s.isCurrent(current) {
		return
	}
	s.sendTurnJSON(current.ctx, current, protocol.TranscriptFinalType, protocol.TextPayload{Text: transcript})

	runtimeTurn.Transcript = transcript
	runtimeTurn.PCM16Mono = pcm
	runtimeTurn.SampleRate = protocol.UplinkSampleRate
	agentCtx := pipeline.WithTurnContext(runtimeCtx, runtimeTurn)
	var reply string
	var ui *pipeline.UICard
	agentStarted := time.Now()
	earlyUI := false
	agentCtx = capability.WithPresentationSink(agentCtx, func(p capability.Presentation) {
		if sendErr := s.sendTurnJSON(current.ctx, current, protocol.UICardType, protocol.UICardPayload{UI: &pipeline.UICard{Kind: p.Kind, Title: p.Title, Primary: p.Primary, Secondary: p.Secondary, Progress: p.Progress}}); sendErr == nil {
			earlyUI = true
		}
	})
	if streaming, ok := s.components.Agent.(pipeline.StreamingAgent); ok {
		metrics, streamErr := s.processStreamingReply(current, agentCtx, transcript, streaming)
		observability.Record(agentCtx, observability.Event{Name: observability.EventTurnStage, Stage: "agent_stream", DurationMS: metrics.AgentTotal.Milliseconds(), Outcome: observabilityOutcome(streamErr)})
		if metrics.FirstSegmentAt > 0 {
			observability.Record(agentCtx, observability.Event{Name: observability.EventTurnStage, Stage: "first_segment", DurationMS: metrics.FirstSegmentAt.Milliseconds(), Outcome: "ok"})
		}
		if metrics.TTSActive > 0 {
			observability.Record(agentCtx, observability.Event{Name: observability.EventTurnStage, Stage: "tts_stream", DurationMS: metrics.TTSActive.Milliseconds(), Outcome: observabilityOutcome(streamErr)})
		}
		if streamErr != nil {
			if !errors.Is(streamErr, context.Canceled) {
				s.failTurn(current, "agent_stream_failed", streamErr)
			}
			return
		}
		observability.Record(agentCtx, observability.Event{Name: observability.EventTurnEnd, DurationMS: time.Since(turnStarted).Milliseconds(), Outcome: "ok"})
		s.logger.Info("streaming voice turn completed",
			"turn_id", current.id, "session_id", s.id, "device_id", s.deviceID, "user_id", s.userID,
			"asr_ms", asrDuration.Milliseconds(), "agent_total_ms", metrics.AgentTotal.Milliseconds(),
			"first_segment_ms", metrics.FirstSegmentAt.Milliseconds(), "tts_active_ms", metrics.TTSActive.Milliseconds(),
			"total_ms", time.Since(turnStarted).Milliseconds())
		s.mu.Lock()
		if s.active == current {
			s.active = nil
		}
		s.mu.Unlock()
		return
	}
	if rich, ok := s.components.Agent.(pipeline.RichAgent); ok {
		result, richErr := rich.RespondRich(agentCtx, current.id, transcript)
		reply, ui, err = result.Text, result.UI, richErr
	} else {
		reply, err = s.components.Agent.Respond(agentCtx, current.id, transcript)
	}
	agentDuration := time.Since(agentStarted)
	observability.Record(agentCtx, observability.Event{Name: observability.EventTurnStage, Stage: "agent", DurationMS: agentDuration.Milliseconds(), Outcome: observabilityOutcome(err)})
	if err != nil {
		s.failTurn(current, "agent_failed", err)
		return
	}
	if !s.isCurrent(current) {
		return
	}
	s.setTurnState(current, "speaking")
	if ui != nil && !earlyUI {
		if err := s.sendTurnJSON(current.ctx, current, protocol.UICardType, protocol.UICardPayload{UI: ui}); err != nil {
			s.failTurn(current, "control_write_failed", err)
			return
		}
	}
	if err := s.sendTurnMediaJSON(current.ctx, current, protocol.TTSLifecycleType, protocol.TTSLifecyclePayload{State: "start"}); err != nil {
		s.failTurn(current, "media_write_failed", err)
		return
	}
	if err := s.sendTurnMediaJSON(current.ctx, current, protocol.TTSLifecycleType, protocol.TTSLifecyclePayload{State: "sentence_start", Text: reply}); err != nil {
		s.failTurn(current, "media_write_failed", err)
		return
	}
	ttsStarted := time.Now()
	err = s.components.TTS.Synthesize(agentCtx, reply, func(frame []byte) error {
		packet, err := s.codec.EncodeDownlink(frame)
		if err != nil {
			return err
		}
		return s.sendTurn(current.ctx, current, outbound{kind: websocket.MessageBinary, data: packet})
	})
	ttsDuration := time.Since(ttsStarted)
	observability.Record(agentCtx, observability.Event{Name: observability.EventTurnStage, Stage: "tts", DurationMS: ttsDuration.Milliseconds(), Outcome: observabilityOutcome(err)})
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.failTurn(current, "tts_failed", err)
		}
		return
	}
	if !s.isCurrent(current) {
		return
	}
	if err := s.sendTurnMediaJSON(current.ctx, current, protocol.TTSLifecycleType, protocol.TTSLifecyclePayload{State: "stop"}); err != nil {
		s.failTurn(current, "media_write_failed", err)
		return
	}
	observability.Record(agentCtx, observability.Event{Name: observability.EventTurnEnd, DurationMS: time.Since(turnStarted).Milliseconds(), Outcome: "ok"})
	s.logger.Info("voice turn completed",
		"turn_id", current.id, "session_id", s.id, "device_id", s.deviceID, "user_id", s.userID,
		"asr_ms", asrDuration.Milliseconds(), "agent_ms", agentDuration.Milliseconds(), "tts_ms", ttsDuration.Milliseconds(), "total_ms", time.Since(turnStarted).Milliseconds())
	s.mu.Lock()
	if s.active == current {
		s.active = nil
	}
	s.mu.Unlock()
}

func (s *session) isCurrent(candidate *turn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active == candidate && s.active.generation == candidate.generation && candidate.ctx.Err() == nil
}

func (s *session) setTurnState(candidate *turn, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == candidate {
		s.active.state = state
	}
}

func waitForTurn(current *turn, timeout time.Duration) bool {
	if current == nil || current.done == nil {
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-current.done:
		return true
	case <-timer.C:
		return false
	}
}

func (s *session) cancelActiveAndJoin(timeout time.Duration) bool {
	s.mu.Lock()
	current := s.active
	if current != nil {
		current.cancel()
		s.active = nil
		s.generation++
	}
	s.mu.Unlock()
	return waitForTurn(current, timeout)
}

func (s *session) interruptActive(ctx context.Context, reason string) {
	s.mu.Lock()
	current := s.active
	if current != nil {
		current.cancel()
		s.active = nil
		s.generation++
	}
	s.mu.Unlock()
	if current != nil {
		joinStarted := time.Now()
		joined := waitForTurn(current, turnCancellationJoinMax)
		joinDuration := time.Since(joinStarted)
		observability.RecordTo(s.observer, observability.Event{
			Name: observability.EventTurnInterrupted, Outcome: "cancelled", Reason: reason, DurationMS: joinDuration.Milliseconds(), Correlation: s.turnCorrelation(current),
		})
		if !joined {
			s.logger.Warn("interrupted turn exceeded cancellation join bound", "session_id", s.id, "turn_id", current.id, "reason", reason, "join_ms", joinDuration.Milliseconds())
		}
		_ = s.sendJSONMeta(ctx, protocol.TurnStateType, protocol.Metadata{
			TurnID: current.id, GenerationID: current.generation,
		}, protocol.TurnStatePayload{State: "interrupted", Reason: reason})
	}
}

func (s *session) generationCurrent(generation uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generation == generation
}

func (s *session) outboundCurrent(message outbound) bool {
	return !message.turnScoped || s.generationCurrent(message.generation)
}

func (s *session) failTurn(current *turn, code string, err error) {
	if !s.isCurrent(current) {
		return
	}
	observability.RecordTo(s.observer, observability.Event{Name: observability.EventTurnEnd, Outcome: "error", Reason: code, Correlation: s.turnCorrelation(current)})
	s.sendTurnError(current.ctx, current, code, err.Error())
	s.mu.Lock()
	if s.active == current {
		s.active = nil
	}
	s.mu.Unlock()
}

func (s *session) nextMessageID() string {
	sequence := s.messageSeq.Add(1)
	return s.id + "-" + strconv.FormatUint(sequence, 10)
}

func (s *session) encodeJSON(messageType protocol.MessageType, metadata protocol.Metadata, payload any) ([]byte, error) {
	if metadata.MessageID == "" {
		metadata.MessageID = s.nextMessageID()
	}
	if metadata.SessionID == "" {
		metadata.SessionID = s.id
	}
	return protocol.Encode(messageType, metadata, payload)
}

func (s *session) sendJSON(ctx context.Context, messageType protocol.MessageType, payload any) error {
	return s.sendJSONMeta(ctx, messageType, protocol.Metadata{}, payload)
}

func (s *session) sendJSONMeta(ctx context.Context, messageType protocol.MessageType, metadata protocol.Metadata, payload any) error {
	data, err := s.encodeJSON(messageType, metadata, payload)
	if err != nil {
		return err
	}
	return s.send(ctx, outbound{kind: websocket.MessageText, data: data})
}

func (s *session) sendTurnJSON(ctx context.Context, current *turn, messageType protocol.MessageType, payload any) error {
	data, err := s.encodeJSON(messageType, protocol.Metadata{
		TurnID: current.id, GenerationID: current.generation,
	}, payload)
	if err != nil {
		return err
	}
	return s.sendTurn(ctx, current, outbound{kind: websocket.MessageText, data: data})
}

func (s *session) sendTurn(ctx context.Context, current *turn, message outbound) error {
	message.turnScoped = true
	message.generation = current.generation
	return s.send(ctx, message)
}

// sendTurnMediaJSON enqueues a turn-scoped TTS lifecycle event on the same
// FIFO lane as its audio frames. Urgent control messages keep their separate
// priority lane, while media causality is explicit rather than dependent on
// scheduler timing across two queues.
func (s *session) sendTurnMediaJSON(ctx context.Context, current *turn, messageType protocol.MessageType, payload any) error {
	if current == nil {
		return fmt.Errorf("turn is required")
	}
	data, err := s.encodeJSON(messageType, protocol.Metadata{
		TurnID: current.id, GenerationID: current.generation,
	}, payload)
	if err != nil {
		return err
	}
	out := outbound{kind: websocket.MessageText, data: data, turnScoped: true, generation: current.generation}
	// Media control is causally required after previously accepted audio. It may
	// wait for bounded queue capacity instead of failing on a transiently-full
	// lane; ordinary audio frame production remains non-blocking via sendTurn.
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	select {
	case <-waitCtx.Done():
		return waitCtx.Err()
	case s.mediaWrites <- out:
		return nil
	}
}

func (s *session) sendTurnError(ctx context.Context, current *turn, code, message string) {
	_ = s.sendTurnJSON(ctx, current, protocol.ProtocolErrorType, protocol.ProtocolErrorPayload{Code: code, Message: message})
}

func (s *session) sendError(ctx context.Context, code, message, turnID string) {
	_ = s.sendJSONMeta(ctx, protocol.ProtocolErrorType, protocol.Metadata{TurnID: turnID}, protocol.ProtocolErrorPayload{Code: code, Message: message})
}

func (s *session) writeHandshakeError(ctx context.Context, code, message, correlationID string) error {
	data, err := s.encodeJSON(protocol.ProtocolErrorType, protocol.Metadata{CorrelationID: correlationID}, protocol.ProtocolErrorPayload{Code: code, Message: message})
	if err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.connection.Write(writeCtx, websocket.MessageText, data)
}

func (s *session) processInbound(messageID string, data []byte, action func() error) error {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("canonicalize message: %w", err)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("canonicalize message: %w", err)
	}
	digest := sha256.Sum256(canonical)
	if s.seenInbound == nil {
		s.seenInbound = make(map[string]inboundRecord)
	}
	if previous, exists := s.seenInbound[messageID]; exists {
		if previous.digest != digest {
			return &protocol.ProtocolError{Code: protocol.InvalidEnvelopeCode, Detail: fmt.Sprintf("message_id %q was reused with different content", messageID)}
		}
		return previous.outcome
	}
	outcome := action()
	if outcome != nil {
		// Failed actions are not replay records: transient pre-commit failures
		// must be retryable with the same message_id in the live session.
		return outcome
	}
	const maximumRememberedMessages = 256
	if len(s.seenOrder) == maximumRememberedMessages {
		delete(s.seenInbound, s.seenOrder[0])
		copy(s.seenOrder, s.seenOrder[1:])
		s.seenOrder = s.seenOrder[:maximumRememberedMessages-1]
	}
	s.seenInbound[messageID] = inboundRecord{digest: digest}
	s.seenOrder = append(s.seenOrder, messageID)
	return nil
}

func (s *session) send(ctx context.Context, message outbound) error {
	queue := s.controlWrites
	label := "control"
	if message.kind == websocket.MessageBinary {
		queue = s.mediaWrites
		label = "audio"
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case queue <- message:
		return nil
	default:
		observability.RecordTo(s.observer, observability.Event{
			Name: observability.EventQueueFull, Outcome: "error", Queue: label, QueueCapacity: cap(queue),
			Correlation: observability.Correlation{SessionID: s.id, GenerationID: message.generation},
		})
		return fmt.Errorf("client %s write queue is full", label)
	}
}

func protocolConfig(c controlplane.RuntimeConfig) protocol.RuntimeConfig {
	return protocol.RuntimeConfig{SmartVADEnabled: c.SmartVADEnabled, VADThreshold: c.VADThreshold, VADSilenceMS: c.VADSilenceMS, VADMinSpeechMS: c.VADMinSpeechMS, IdleAfterMS: c.IdleAfterMS, AlarmVisibleMS: c.AlarmVisibleMS, Locale: c.Locale, Timezone: c.Timezone, VoiceKey: c.VoiceKey}
}
func controlConfig(c protocol.RuntimeConfig) controlplane.RuntimeConfig {
	return controlplane.RuntimeConfig{SmartVADEnabled: c.SmartVADEnabled, VADThreshold: c.VADThreshold, VADSilenceMS: c.VADSilenceMS, VADMinSpeechMS: c.VADMinSpeechMS, IdleAfterMS: c.IdleAfterMS, AlarmVisibleMS: c.AlarmVisibleMS, Locale: c.Locale, Timezone: c.Timezone, VoiceKey: c.VoiceKey}
}
func (s *Server) adminOK(r *http.Request) bool {
	return s.adminToken != "" && r.Header.Get("Authorization") == "Bearer "+s.adminToken
}
func (s *Server) handleTwinGet(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	user := strings.TrimSpace(r.URL.Query().Get("user_id"))
	if user == "" {
		user = "default"
	}
	t, e := s.controlPlane.Manifest(r.Context(), user, r.PathValue("deviceID"))
	if e != nil {
		http.Error(w, e.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(t)
}
func (s *Server) handleTwinPatch(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	user := strings.TrimSpace(r.URL.Query().Get("user_id"))
	if user == "" {
		user = "default"
	}
	var c controlplane.RuntimeConfig
	if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&c); e != nil {
		http.Error(w, e.Error(), http.StatusBadRequest)
		return
	}
	t, e := s.controlPlane.SetDesired(r.Context(), user, r.PathValue("deviceID"), c)
	if e != nil {
		http.Error(w, e.Error(), http.StatusBadRequest)
		return
	}
	if sess := s.hub.get(r.PathValue("deviceID")); sess != nil {
		pc := protocolConfig(t.Desired)
		_ = sess.sendJSON(r.Context(), protocol.ConfigUpdateType, protocol.ConfigUpdatePayload{Config: pc, ConfigVersion: t.DesiredVersion})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(t)
}

func (s *Server) handleConfigSchema(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(controlplane.ConfigSchema())
}
func (s *Server) handleScopedConfigGet(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	c, ok, e := s.controlPlane.GetScopedConfig(r.Context(), r.PathValue("scopeType"), r.PathValue("scopeID"))
	if e != nil {
		http.Error(w, e.Error(), http.StatusBadRequest)
		return
	}
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(c)
}
func (s *Server) handleScopedConfigPatch(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var c controlplane.RuntimeConfig
	if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&c); e != nil {
		http.Error(w, e.Error(), http.StatusBadRequest)
		return
	}
	scopeType, scopeID := r.PathValue("scopeType"), r.PathValue("scopeID")
	if e := s.controlPlane.SetScopedConfig(r.Context(), scopeType, scopeID, c); e != nil {
		http.Error(w, e.Error(), http.StatusBadRequest)
		return
	}
	s.pushResolvedConfig(r.Context(), scopeType, scopeID)
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) pushResolvedConfig(ctx context.Context, scopeType, scopeID string) {
	for _, id := range s.hub.identities() {
		if scopeType == "user" && id.UserID != scopeID {
			continue
		}
		if scopeType == "tenant" && id.TenantID != scopeID {
			continue
		}
		if scopeType == "plan" && id.Plan != scopeID {
			continue
		}
		if scopeType != "global" && scopeType != "user" && scopeType != "tenant" && scopeType != "plan" {
			continue
		}
		if t, e := s.controlPlane.ManifestFor(ctx, controlplane.ResolutionContext{UserID: id.UserID, DeviceID: id.DeviceID, TenantID: id.TenantID, Plan: id.Plan}); e == nil {
			pc := protocolConfig(t.Desired)
			for _, sess := range s.hub.targets(id.UserID, id.DeviceID) {
				_ = sess.sendJSON(ctx, protocol.ConfigUpdateType, protocol.ConfigUpdatePayload{Config: pc, ConfigVersion: t.DesiredVersion})
			}
		}
	}
}
func (s *Server) handleFeaturesGet(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	xs, e := s.controlPlane.Flags(r.Context())
	if e != nil {
		http.Error(w, e.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(xs)
}
func (s *Server) handleFeaturePatch(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var f controlplane.Flag
	if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&f); e != nil {
		http.Error(w, e.Error(), http.StatusBadRequest)
		return
	}
	f.Key = r.PathValue("key")
	if e := s.controlPlane.SetFlag(r.Context(), f); e != nil {
		http.Error(w, e.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) handleEntitlementPatch(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var b struct {
		Enabled   bool   `json:"enabled"`
		ExpiresAt string `json:"expires_at"`
	}
	if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&b); e != nil {
		http.Error(w, e.Error(), http.StatusBadRequest)
		return
	}
	var exp *time.Time
	if b.ExpiresAt != "" {
		t, e := time.Parse(time.RFC3339, b.ExpiresAt)
		if e != nil {
			http.Error(w, "expires_at must be RFC3339", http.StatusBadRequest)
			return
		}
		exp = &t
	}
	if e := s.entitlements.SetEntitlement(r.Context(), r.PathValue("userID"), r.PathValue("key"), b.Enabled, exp); e != nil {
		http.Error(w, e.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCredentialEnroll(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var body struct {
		UserID   string `json:"user_id"`
		TenantID string `json:"tenant_id"`
		Plan     string `json:"plan"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body)
	if strings.TrimSpace(body.UserID) == "" {
		body.UserID = "default"
	}
	buf := make([]byte, 32)
	if _, e := rand.Read(buf); e != nil {
		http.Error(w, "credential generation failed", http.StatusInternalServerError)
		return
	}
	token := hex.EncodeToString(buf)
	identity := domain.Identity{UserID: body.UserID, DeviceID: r.PathValue("deviceID"), TenantID: strings.TrimSpace(body.TenantID), Plan: strings.TrimSpace(body.Plan)}
	if e := s.credentials.EnrollDevice(r.Context(), identity, token); e != nil {
		http.Error(w, e.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"device_id": r.PathValue("deviceID"), "user_id": identity.UserID, "tenant_id": identity.TenantID, "plan": identity.Plan, "token": token, "shown_once": true})
}
func (s *Server) handleCredentialRevoke(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if e := s.credentials.RevokeDevice(r.Context(), r.PathValue("deviceID")); e != nil {
		http.Error(w, e.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleModulesGet(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	xs, err := s.featureCatalog.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(xs)
}
func (s *Server) handleModulePut(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var m controlplane.FeatureModule
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&m); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	m.ID = r.PathValue("id")
	if err := s.featureCatalog.Put(r.Context(), m); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePrivacyGet(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	p, err := s.privacy.Policy(r.Context(), r.PathValue("userID"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(p)
}
func (s *Server) handlePrivacyPatch(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	current, err := s.privacy.Policy(r.Context(), r.PathValue("userID"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Full policy replacement is deliberate: privacy booleans must not get
	// ambiguous zero-value PATCH semantics.
	var p privacy.Policy
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&p); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	p.UserID = current.UserID
	if err := s.privacy.Set(r.Context(), p); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleFirmwarePublish(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var manifest controlplane.FirmwareManifest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&manifest); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.firmware.Publish(r.Context(), manifest); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) handleOTAGet(w http.ResponseWriter, r *http.Request) {
	_, ok := s.authenticateDeviceRequest(r.Context(), r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	board := strings.TrimSpace(r.URL.Query().Get("board"))
	channel := strings.TrimSpace(r.URL.Query().Get("channel"))
	if channel == "" {
		channel = "stable"
	}
	protocolVersion, err := strconv.Atoi(r.URL.Query().Get("protocol"))
	if err != nil || protocolVersion <= 0 || board == "" {
		http.Error(w, "board and positive protocol are required", http.StatusBadRequest)
		return
	}
	securityVersion, err := optionalInt(r.URL.Query().Get("security_version"))
	if err != nil {
		http.Error(w, "security_version must be an integer", http.StatusBadRequest)
		return
	}
	metadataVersion, err := optionalInt64(r.URL.Query().Get("metadata_version"))
	if err != nil {
		http.Error(w, "metadata_version must be an integer", http.StatusBadRequest)
		return
	}
	manifest, found, err := s.firmware.Latest(r.Context(), channel, board, protocolVersion, securityVersion, metadataVersion)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(manifest)
}

func optionalInt(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	return strconv.Atoi(raw)
}
func optionalInt64(raw string) (int64, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	return strconv.ParseInt(raw, 10, 64)
}

func parseAlarmID(value string) (int64, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "reminder-")
	value = strings.TrimPrefix(value, "schedule-")
	if value == "" {
		return 0, fmt.Errorf("alarm id is required")
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid alarm id %q", value)
	}
	return id, nil
}

func randomID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("session-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buffer)
}
