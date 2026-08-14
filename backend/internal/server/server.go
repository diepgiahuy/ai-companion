package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"companion-server/internal/controlplane"
	"companion-server/internal/domain"
	"companion-server/internal/observability"
	"companion-server/internal/pipeline"
	"companion-server/internal/privacy"
	"companion-server/internal/protocol"
	"companion-server/internal/supervision"
)

const (
	writeQueueDepth       = 64
	maximumInboundMessage = 64 << 10
	turnCancellationJoinMax = 2 * time.Second
)

type sessionHub struct {
	mu       sync.Mutex
	sessions map[string]*session
}

func newSessionHub() *sessionHub { return &sessionHub{sessions: map[string]*session{}} }
func (h *sessionHub) register(s *session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sessions[s.id] = s
}
func (h *sessionHub) unregister(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.sessions, id)
}
func (h *sessionHub) get(deviceID string) *session {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, s := range h.sessions {
		if s.deviceID == deviceID {
			return s
		}
	}
	return nil
}
func (h *sessionHub) targets(userID, deviceID string) []*session {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*session, 0)
	for _, s := range h.sessions {
		if deviceID != "" && s.deviceID != deviceID {
			continue
		}
		if userID != "" && s.userID != userID {
			continue
		}
		out = append(out, s)
	}
	return out
}
func (h *sessionHub) identities() []domain.Identity {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]domain.Identity, 0, len(h.sessions))
	for _, s := range h.sessions {
		out = append(out, s.identity)
	}
	return out
}

// Server is the production-shaped voice transport and control plane.
type Server struct {
	components     pipeline.Components
	logger         *slog.Logger
	location       *time.Location
	identity       IdentityResolver
	deviceAuth     DeviceAuthenticator
	controlPlane   ControlPlane
	firmware       FirmwareService
	privacy        PrivacyService
	featureCatalog FeatureCatalog
	credentials    DeviceCredentialManager
	entitlements   EntitlementManager
	adminToken     string
	observer       observability.Recorder
	hub            *sessionHub
	mux            *http.ServeMux
	store          ScheduleStore
	now            func() time.Time
}

type Option func(*Server)

func WithStore(s ScheduleStore) Option { return func(server *Server) { server.store = s } }
func WithLocation(location *time.Location) Option {
	return func(server *Server) { server.location = location }
}
func WithIdentityResolver(resolver IdentityResolver) Option {
	return func(server *Server) { server.identity = resolver }
}
func WithDeviceAuthenticator(authenticator DeviceAuthenticator) Option {
	return func(server *Server) { server.deviceAuth = authenticator }
}
func WithControlPlane(control ControlPlane) Option {
	return func(server *Server) { server.controlPlane = control }
}
func WithFirmwareService(firmware FirmwareService) Option {
	return func(server *Server) { server.firmware = firmware }
}
func WithPrivacyService(service PrivacyService) Option {
	return func(server *Server) { server.privacy = service }
}
func WithFeatureCatalog(catalog FeatureCatalog) Option {
	return func(server *Server) { server.featureCatalog = catalog }
}
func WithAdminToken(token string) Option {
	return func(server *Server) { server.adminToken = strings.TrimSpace(token) }
}
func WithDeviceCredentialManager(manager DeviceCredentialManager) Option {
	return func(server *Server) { server.credentials = manager }
}
func WithEntitlementManager(manager EntitlementManager) Option {
	return func(server *Server) { server.entitlements = manager }
}
func WithObservabilityRecorder(recorder observability.Recorder) Option {
	return func(server *Server) {
		if recorder != nil {
			server.observer = recorder
		}
	}
}

func New(components pipeline.Components, logger *slog.Logger, options ...Option) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		components:     components,
		logger:         logger,
		location:       time.Local,
		identity:       HeaderIdentityResolver{DefaultUserID: "default"},
		deviceAuth:     DenyDeviceAuthenticator{},
		controlPlane:   noopControlPlane{},
		firmware:       noopFirmware{},
		privacy:        noopPrivacy{},
		featureCatalog: noopFeatureCatalog{},
		credentials:    noopCredentials{},
		entitlements:   noopEntitlements{},
		observer:       observability.Nop(),
		hub:            newSessionHub(),
		mux:            http.NewServeMux(),
		now:            time.Now,
	}
	for _, option := range options {
		option(s)
	}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	s.mux.HandleFunc("GET /v2/device", s.handleDevice)
	s.mux.HandleFunc("GET /v1/ota/latest", s.handleOTAGet)
	s.mux.HandleFunc("GET /v1/admin/devices/{deviceID}/twin", s.handleTwinGet)
	s.mux.HandleFunc("PATCH /v1/admin/devices/{deviceID}/twin", s.handleTwinPatch)
	s.mux.HandleFunc("GET /v1/admin/config/schema", s.handleConfigSchema)
	s.mux.HandleFunc("GET /v1/admin/config/{scopeType}/{scopeID}", s.handleScopedConfigGet)
	s.mux.HandleFunc("PATCH /v1/admin/config/{scopeType}/{scopeID}", s.handleScopedConfigPatch)
	s.mux.HandleFunc("GET /v1/admin/features", s.handleFeaturesGet)
	s.mux.HandleFunc("PATCH /v1/admin/features/{key}", s.handleFeaturePatch)
	s.mux.HandleFunc("PATCH /v1/admin/users/{userID}/entitlements/{key}", s.handleEntitlementPatch)
	s.mux.HandleFunc("POST /v1/admin/devices/{deviceID}/credential", s.handleCredentialEnroll)
	s.mux.HandleFunc("DELETE /v1/admin/devices/{deviceID}/credential", s.handleCredentialRevoke)
	s.mux.HandleFunc("GET /v1/admin/modules", s.handleModulesGet)
	s.mux.HandleFunc("PUT /v1/admin/modules/{id}", s.handleModulePut)
	s.mux.HandleFunc("GET /v1/admin/users/{userID}/privacy", s.handlePrivacyGet)
	s.mux.HandleFunc("PATCH /v1/admin/users/{userID}/privacy", s.handlePrivacyPatch)
	s.mux.HandleFunc("POST /v1/admin/firmware", s.handleFirmwarePublish)
}

func (s *Server) RunBackground(ctx context.Context) {
	if s.store == nil {
		<-ctx.Done()
		return
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			claimed, err := s.store.ClaimDue(context.Background(), now, 16)
			if err != nil {
				s.logger.Warn("claim schedules", "error", err)
				continue
			}
			for _, item := range claimed {
				if err := s.deliver(item, now); err != nil {
					attempts := item.Attempts + 1
					backoff := time.Duration(attempts*attempts) * time.Second
					if attempts >= 5 {
						backoff = time.Minute
					}
					_ = s.store.FailDelivery(context.Background(), item.ID, err.Error(), now.Add(backoff))
					continue
				}
				_ = s.store.CompleteDelivery(context.Background(), item.ID)
			}
		}
	}
}

func (s *Server) deliver(item domain.ScheduledItem, now time.Time) error {
	payload := protocol.AlarmPayload{ID: fmt.Sprintf("schedule-%d", item.ID), Title: item.Title, FiredAt: now.In(s.location).Format(time.RFC3339)}
	targets := s.hub.targets(item.UserID, item.DeviceID)
	if len(targets) == 0 {
		return fmt.Errorf("no connected target")
	}
	var first error
	for _, target := range targets {
		if err := target.sendJSON(context.Background(), protocol.AlarmType, payload); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (s *Server) handleDevice(w http.ResponseWriter, r *http.Request) {
	identity, ok := s.authenticateDeviceRequest(r.Context(), r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		s.logger.Warn("websocket accept failed", "error", err)
		return
	}
	defer connection.CloseNow()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	identity = s.identity.Resolve(r, identity.DeviceID)
	identity.UserID = strings.TrimSpace(identity.UserID)
	if identity.UserID == "" {
		identity.UserID = "default"
	}
	identity.DeviceID = strings.TrimSpace(identity.DeviceID)
	identity.TenantID = strings.TrimSpace(identity.TenantID)
	identity.Plan = strings.TrimSpace(identity.Plan)
	authenticated, ok := s.authenticateDeviceRequest(ctx, r)
	if !ok {
		_ = connection.Close(websocket.StatusPolicyViolation, "device credential changed during upgrade")
		return
	}
	identity.UserID = authenticated.UserID
	identity.DeviceID = authenticated.DeviceID
	identity.TenantID = authenticated.TenantID
	identity.Plan = authenticated.Plan

	sessionID := randomID()
	sess := &session{
		id:            sessionID,
		deviceID:      identity.DeviceID,
		userID:        identity.UserID,
		identity:      identity,
		connection:    connection,
		components:    s.components,
		logger:        s.logger,
		observer:      s.observer,
		controlWrites: make(chan outbound, writeQueueDepth),
		mediaWrites:   make(chan outbound, writeQueueDepth),
		seenInbound:   make(map[string]inboundRecord),
		seenOrder:     make([]string, 0, 256),
	}
	s.hub.register(sess)
	defer s.hub.unregister(sessionID)
	if err := sess.run(ctx, s.controlPlane); err != nil && !errors.Is(err, context.Canceled) && !websocket.CloseStatus(err).IsValid() {
		s.logger.Warn("device session ended", "error", err, "session_id", sessionID, "device_id", identity.DeviceID)
	}
}

func (s *Server) authenticateDeviceRequest(ctx context.Context, request *http.Request) (domain.Identity, bool) {
	deviceID := strings.TrimSpace(request.Header.Get("Device-Id"))
	authorization := strings.TrimSpace(request.Header.Get("Authorization"))
	if deviceID == "" || !strings.HasPrefix(authorization, "Bearer ") {
		return domain.Identity{}, false
	}
	token := strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
	if token == "" {
		return domain.Identity{}, false
	}
	identity, err := s.deviceAuth.AuthenticateDevice(ctx, deviceID, token)
	if err != nil {
		return domain.Identity{}, false
	}
	return identity, true
}

type outbound struct {
	kind       websocket.MessageType
	data       []byte
	turnScoped bool
	generation uint64
}

type inboundRecord struct {
	digest  [32]byte
	outcome error
}

type turn struct {
	id         string
	generation uint64
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	mode       string
	state      string
	decoder    pipeline.Decoder
	pcm        []byte
}

type session struct {
	id         string
	deviceID   string
	userID     string
	identity   domain.Identity
	connection *websocket.Conn
	components pipeline.Components
	logger     *slog.Logger
	observer   observability.Recorder

	controlWrites chan outbound
	mediaWrites   chan outbound
	messageSeq    atomic.Uint64

	mu         sync.Mutex
	generation uint64
	active     *turn
	seenInbound map[string]inboundRecord
	seenOrder   []string
}

func (s *session) run(ctx context.Context, control ControlPlane) error {
	ready, _, err := s.readEnvelope(ctx)
	if err != nil {
		return err
	}
	if ready.Type != protocol.HelloType {
		_ = s.writeHandshakeError(ctx, protocol.InvalidStateCode, "first message must be hello", ready.CorrelationID)
		return fmt.Errorf("first message must be hello")
	}
	var hello protocol.HelloPayload
	if err := json.Unmarshal(ready.Payload, &hello); err != nil {
		_ = s.writeHandshakeError(ctx, protocol.InvalidPayloadCode, "invalid hello payload", ready.CorrelationID)
		return err
	}
	if hello.ProtocolVersion != protocol.Version {
		_ = s.writeHandshakeError(ctx, protocol.UnsupportedProtocolCode, fmt.Sprintf("protocol v%d required", protocol.Version), ready.CorrelationID)
		return fmt.Errorf("unsupported protocol version")
	}
	if strings.TrimSpace(hello.DeviceID) != "" && strings.TrimSpace(hello.DeviceID) != s.deviceID {
		_ = s.writeHandshakeError(ctx, protocol.InvalidPayloadCode, "hello device_id does not match authenticated device", ready.CorrelationID)
		return fmt.Errorf("hello device_id does not match authenticated device")
	}

	resolved, err := control.ManifestFor(ctx, controlplane.ResolutionContext{UserID: s.identity.UserID, DeviceID: s.identity.DeviceID, TenantID: s.identity.TenantID, Plan: s.identity.Plan})
	if err != nil {
		return err
	}
	config := protocolConfig(resolved.Desired)
	if err := s.connection.Write(ctx, websocket.MessageText, mustEncode(protocol.SessionReadyType, protocol.Metadata{SessionID: s.id, CorrelationID: ready.CorrelationID}, protocol.SessionReadyPayload{SessionID: s.id, DeviceID: s.deviceID, ProtocolVersion: protocol.Version, Config: config, ConfigVersion: resolved.DesiredVersion})); err != nil {
		return err
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	workerGroup := supervision.New(runCtx)
	writeDone := workerGroup.Go("session-write", true, func(workerCtx context.Context) error {
		return s.writeLoop(workerCtx)
	})
	readDone := workerGroup.Go("session-read", true, func(workerCtx context.Context) error {
		return s.readLoop(workerCtx)
	})
	var runErr error
	select {
	case result := <-readDone:
		runErr = result.Err
		cancel()
	case result := <-writeDone:
		runErr = result.Err
		cancel()
	case <-ctx.Done():
		runErr = ctx.Err()
		cancel()
	}
	joined := workerGroup.Wait(3 * time.Second)
	turnJoined := s.cancelActiveAndJoin(turnCancellationJoinMax)
	if !joined || !turnJoined {
		s.logger.Warn("device session workers exceeded shutdown bound", "session_id", s.id, "workers_joined", joined, "turn_joined", turnJoined)
	}
	if runErr == nil {
		runErr = workerGroup.Err()
	}
	return runErr
}

func mustEncode(messageType protocol.MessageType, metadata protocol.Metadata, payload any) []byte {
	data, err := protocol.Encode(messageType, metadata, payload)
	if err != nil {
		panic(err)
	}
	return data
}

func (s *session) readLoop(ctx context.Context) error {
	for {
		envelope, kind, err := s.readEnvelope(ctx)
		if err != nil {
			return err
		}
		if kind == websocket.MessageBinary {
			if err := s.handleBinary(ctx, envelope.Payload); err != nil {
				return err
			}
			continue
		}
		if err := s.handleEnvelope(ctx, envelope); err != nil {
			var protocolError *protocol.ProtocolError
			if errors.As(err, &protocolError) {
				s.sendError(ctx, protocolError.Code, protocolError.Detail, envelope.TurnID)
				continue
			}
			return err
		}
	}
}

func (s *session) readEnvelope(ctx context.Context) (protocol.Envelope, websocket.MessageType, error) {
	kind, data, err := s.connection.Read(ctx)
	if err != nil {
		return protocol.Envelope{}, 0, err
	}
	if len(data) > maximumInboundMessage {
		return protocol.Envelope{}, 0, fmt.Errorf("message exceeds maximum size")
	}
	if kind == websocket.MessageBinary {
		return protocol.Envelope{Payload: data}, kind, nil
	}
	envelope, err := protocol.Decode(data)
	return envelope, kind, err
}

func (s *session) writeLoop(ctx context.Context) error {
	for {
		var message outbound
		select {
		case <-ctx.Done():
			return ctx.Err()
		case message = <-s.controlWrites:
		default:
			select {
			case <-ctx.Done():
				return ctx.Err()
			case message = <-s.controlWrites:
			case message = <-s.mediaWrites:
			}
		}
		if !s.outboundCurrent(message) {
			continue
		}
		writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := s.connection.Write(writeCtx, message.kind, message.data)
		cancel()
		if err != nil {
			return err
		}
	}
}

func (s *session) handleEnvelope(ctx context.Context, envelope protocol.Envelope) error {
	switch envelope.Type {
	case protocol.ListenStartType:
		var payload protocol.ListenStartPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return &protocol.ProtocolError{Code: protocol.InvalidPayloadCode, Detail: err.Error()}
		}
		return s.processInbound(envelope.MessageID, mustEncode(envelope.Type, envelope.Metadata, payload), func() error {
			return s.startTurn(ctx, envelope.TurnID, payload.Mode)
		})
	case protocol.ListenStopType:
		return s.processInbound(envelope.MessageID, mustEncode(envelope.Type, envelope.Metadata, map[string]any{}), func() error {
			return s.finishTurn(ctx, envelope.TurnID)
		})
	case protocol.AbortType:
		var payload protocol.AbortPayload
		if len(envelope.Payload) != 0 && string(envelope.Payload) != "null" {
			if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
				return &protocol.ProtocolError{Code: protocol.InvalidPayloadCode, Detail: err.Error()}
			}
		}
		return s.processInbound(envelope.MessageID, mustEncode(envelope.Type, envelope.Metadata, payload), func() error {
			s.interruptActive(ctx, payload.Reason)
			return nil
		})
	case protocol.ConfigAppliedType:
		var payload protocol.ConfigAppliedPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return &protocol.ProtocolError{Code: protocol.InvalidPayloadCode, Detail: err.Error()}
		}
		return s.processInbound(envelope.MessageID, mustEncode(envelope.Type, envelope.Metadata, payload), func() error {
			return s.handleConfigApplied(ctx, payload)
		})
	case protocol.AlarmAckType:
		var payload protocol.AlarmAckPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return &protocol.ProtocolError{Code: protocol.InvalidPayloadCode, Detail: err.Error()}
		}
		return s.processInbound(envelope.MessageID, mustEncode(envelope.Type, envelope.Metadata, payload), func() error {
			return s.handleAlarmAck(ctx, payload)
		})
	default:
		return &protocol.ProtocolError{Code: protocol.UnknownMessageTypeCode, Detail: fmt.Sprintf("unsupported type %q", envelope.Type)}
	}
}

func (s *session) handleBinary(ctx context.Context, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	s.mu.Lock()
	current := s.active
	if current == nil {
		s.mu.Unlock()
		return &protocol.ProtocolError{Code: protocol.InvalidStateCode, Detail: "audio received without active turn"}
	}
	decoder := current.decoder
	s.mu.Unlock()

	pcm, err := decoder.Decode(data)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.active != current || current.ctx.Err() != nil {
		s.mu.Unlock()
		return nil
	}
	current.pcm = append(current.pcm, pcm...)
	s.mu.Unlock()
	return nil
}

func (s *session) startTurn(parent context.Context, turnID, mode string) error {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return &protocol.ProtocolError{Code: protocol.InvalidPayloadCode, Detail: "turn_id is required"}
	}
	mode = strings.TrimSpace(mode)
	if mode != "manual" && mode != "auto_vad" {
		return &protocol.ProtocolError{Code: protocol.InvalidPayloadCode, Detail: "listen mode must be manual or auto_vad"}
	}
	decoder, err := s.components.Codecs.NewDecoder()
	if err != nil {
		return err
	}

	s.interruptActive(parent, "new_turn")
	s.mu.Lock()
	s.generation++
	turnCtx, cancel := context.WithCancel(parent)
	current := &turn{id: turnID, generation: s.generation, ctx: turnCtx, cancel: cancel, done: make(chan struct{}), mode: mode, state: "listening", decoder: decoder}
	s.active = current
	s.mu.Unlock()
	return s.sendTurnJSON(parent, current, protocol.TurnStateType, protocol.TurnStatePayload{State: "listening"})
}

func (s *session) finishTurn(parent context.Context, turnID string) error {
	s.mu.Lock()
	current := s.active
	if current == nil || current.id != turnID {
		s.mu.Unlock()
		return &protocol.ProtocolError{Code: protocol.InvalidStateCode, Detail: "listen.stop does not match active turn"}
	}
	if current.state != "listening" {
		s.mu.Unlock()
		return &protocol.ProtocolError{Code: protocol.InvalidStateCode, Detail: "turn is not listening"}
	}
	if len(current.pcm) == 0 {
		s.mu.Unlock()
		return &protocol.ProtocolError{Code: protocol.InvalidStateCode, Detail: "no audio for turn"}
	}
	current.state = "processing"
	s.mu.Unlock()
	go s.processTurn(current)
	return s.sendTurnJSON(parent, current, protocol.TurnStateType, protocol.TurnStatePayload{State: "processing"})
}

func (s *session) processTurn(current *turn) {
	defer close(current.done)
	turnStarted := time.Now()
	turnCtx := pipeline.WithTurnContext(current.ctx, pipeline.TurnContext{
		UserID: s.userID, TenantID: s.identity.TenantID, Plan: s.identity.Plan, DeviceID: s.deviceID,
		SessionID: s.id, TurnID: current.id, Generation: current.generation, PCM16Mono: append([]byte(nil), current.pcm...),
	})
	correlation := s.turnCorrelation(current)
	observability.RecordTo(s.observer, observability.Event{Name: observability.EventTurnStart, Outcome: "ok", Correlation: correlation})
	asrStarted := time.Now()
	transcript, err := s.components.ASR.Transcribe(turnCtx, current.pcm)
	asrDuration := time.Since(asrStarted)
	if err != nil {
		s.failTurn(current, "asr_failed", err)
		return
	}
	if !s.isCurrent(current) {
		return
	}
	turnCtx = pipeline.WithTurnContext(turnCtx, pipeline.TurnContext{
		UserID: s.userID, TenantID: s.identity.TenantID, Plan: s.identity.Plan, DeviceID: s.deviceID,
		SessionID: s.id, TurnID: current.id, Generation: current.generation, PCM16Mono: append([]byte(nil), current.pcm...), Transcript: transcript,
	})
	_ = s.sendTurnJSON(current.ctx, current, protocol.STTPartialType, protocol.STTPartialPayload{Text: transcript, Final: true})
	s.setTurnState(current, "agent")
	_ = s.sendTurnJSON(current.ctx, current, protocol.TurnStateType, protocol.TurnStatePayload{State: "agent"})
	agentStarted := time.Now()
	var response string
	if streaming, ok := s.components.Agent.(pipeline.StreamingAgent); ok {
		var builder strings.Builder
		err = streaming.RespondStream(turnCtx, transcript, func(delta string) error {
			if !s.isCurrent(current) {
				return context.Canceled
			}
			builder.WriteString(delta)
			return nil
		})
		response = builder.String()
	} else {
		response, err = s.components.Agent.Respond(turnCtx, transcript)
	}
	agentDuration := time.Since(agentStarted)
	if err != nil {
		s.failTurn(current, "agent_failed", err)
		return
	}
	if !s.isCurrent(current) {
		return
	}
	s.setTurnState(current, "tts")
	_ = s.sendTurnJSON(current.ctx, current, protocol.TurnStateType, protocol.TurnStatePayload{State: "tts"})
	ttsStarted := time.Now()
	started := false
	streamErr := s.components.TTS.StreamPCM(turnCtx, response, func(pcm []int16) error {
		if !s.isCurrent(current) {
			return context.Canceled
		}
		if !started {
			started = true
			if err := s.sendTurnMediaJSON(current.ctx, current, protocol.TTSStartType, protocol.TTSStartPayload{SampleRateHz: 24000}); err != nil {
				return err
			}
		}
		if err := s.sendTurn(current.ctx, current, outbound{kind: websocket.MessageBinary, data: pipeline.PCM16Bytes(pcm)}); err != nil {
			return err
		}
		return nil
	})
	ttsDuration := time.Since(ttsStarted)
	if streamErr != nil {
		s.failTurn(current, "tts_failed", streamErr)
		return
	}
	if !s.isCurrent(current) {
		return
	}
	if !started {
		if err := s.sendTurnMediaJSON(current.ctx, current, protocol.TTSStartType, protocol.TTSStartPayload{SampleRateHz: 24000}); err != nil {
			s.failTurn(current, "tts_start_failed", err)
			return
		}
	}
	if err := s.sendTurnMediaJSON(current.ctx, current, protocol.TTSSentenceType, protocol.TTSSentencePayload{Text: response}); err != nil {
		s.failTurn(current, "tts_sentence_failed", err)
		return
	}
	if err := s.sendTurnMediaJSON(current.ctx, current, protocol.TTSStopType, protocol.TTSStopPayload{}); err != nil {
		s.failTurn(current, "tts_stop_failed", err)
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
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := waitCtx.Err(); err != nil {
		return err
	}
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
	if err := ctx.Err(); err != nil {
		return err
	}
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