package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"companion-server/internal/events"
	"companion-server/internal/idempotency"
	"companion-server/internal/pairing"
	"companion-server/internal/protocol"
	"companion-server/internal/voicemail"
)

const maximumRecoveredVoiceMail = 4

func (s *session) recoverUnreadVoiceMail(ctx context.Context) error {
	if s.voiceMail == nil {
		return nil
	}
	items, err := s.voiceMail.ListUnread(ctx, s.userID, s.deviceID, maximumRecoveredVoiceMail)
	if err != nil {
		return fmt.Errorf("list unread: %w", err)
	}
	for _, item := range items {
		message := protocol.VoiceMailAvailable{
			VoiceMailID: item.ID, FromDeviceID: item.SenderDeviceID,
			MediaFormat: item.MediaFormat, DurationMS: item.DurationMS,
			SizeBytes: item.SizeBytes, ChecksumSHA256: item.ChecksumSHA256,
			ExpiresAt: item.ExpiresAt, Policy: protocol.VoiceMailPolicy(item.Policy),
		}
		if err := message.Validate(); err != nil {
			return fmt.Errorf("validate unread %q: %w", item.ID, err)
		}
		occurredAt := item.UpdatedAt
		if occurredAt.IsZero() {
			occurredAt = time.Now().UTC()
		}
		digest := sha256.Sum256([]byte(item.ID + "\x00" + item.UpdatedAt.UTC().Format(time.RFC3339Nano)))
		metadata := protocol.Metadata{
			IdempotencyKey: fmt.Sprintf("voice-mail-recovery-%x", digest[:12]),
			OccurredAt:     occurredAt.UTC().Format(time.RFC3339Nano),
		}
		if err := s.sendJSONMeta(ctx, protocol.VoiceMailAvailableType, metadata, message); err != nil {
			return fmt.Errorf("send unread %q: %w", item.ID, err)
		}
	}
	return nil
}

func voiceMailRequest(actor, operation, key string, value any) (idempotency.Request, error) {
	hash, err := idempotency.HashValue(value)
	if err != nil {
		return idempotency.Request{}, err
	}
	request := idempotency.Request{Actor: strings.TrimSpace(actor), Operation: operation, Key: strings.TrimSpace(key), RequestHash: hash}
	return request, request.Validate()
}

func (s *Server) voiceMailIdentity(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	identity, ok := s.authenticateDeviceRequest(r.Context(), r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return "", "", false
	}
	if !s.allowVoiceMailRequest(identity.UserID+"\x00"+identity.DeviceID, time.Now().UTC()) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return "", "", false
	}
	return identity.UserID, identity.DeviceID, true
}

type voiceMailRateWindow struct {
	Started time.Time
	Count   int
}

func (s *Server) allowVoiceMailRequest(actor string, now time.Time) bool {
	const maximumPerMinute = 120
	s.voiceMailRateMu.Lock()
	defer s.voiceMailRateMu.Unlock()
	window := s.voiceMailRates[actor]
	if window.Started.IsZero() || now.Sub(window.Started) >= time.Minute {
		window = voiceMailRateWindow{Started: now}
	}
	if window.Count >= maximumPerMinute {
		s.voiceMailRates[actor] = window
		return false
	}
	window.Count++
	s.voiceMailRates[actor] = window
	if len(s.voiceMailRates) > 4096 {
		for key, value := range s.voiceMailRates {
			if now.Sub(value.Started) >= time.Minute {
				delete(s.voiceMailRates, key)
			}
		}
	}
	return true
}

func (s *Server) handleVoiceMailRecipients(w http.ResponseWriter, r *http.Request) {
	userID, deviceID, ok := s.voiceMailIdentity(w, r)
	if !ok {
		return
	}
	recipients, err := s.voiceMail.ListRecipients(r.Context(), userID, deviceID)
	if err != nil {
		voiceMailError(w, err)
		return
	}
	if recipients == nil {
		recipients = []pairing.RecipientDescriptor{}
	}
	writeVoiceMailJSON(w, http.StatusOK, map[string]any{"recipients": recipients})
}

type voiceMailCreateRequest struct {
	RelationshipID string           `json:"relationship_id"`
	DurationMS     int64            `json:"duration_ms"`
	SizeBytes      int64            `json:"size_bytes"`
	ChecksumSHA256 string           `json:"checksum_sha256"`
	Policy         voicemail.Policy `json:"policy"`
	ExpiresAt      time.Time        `json:"expires_at"`
	IdempotencyKey string           `json:"idempotency_key"`
}

func (s *Server) handleVoiceMailCreate(w http.ResponseWriter, r *http.Request) {
	userID, deviceID, ok := s.voiceMailIdentity(w, r)
	if !ok {
		return
	}
	var body voiceMailCreateRequest
	if err := decodeVoiceMailJSON(w, r, &body); err != nil {
		return
	}
	create := voicemail.Create{
		RelationshipID: body.RelationshipID,
		SenderUserID:   userID,
		SenderDeviceID: deviceID,
		DurationMS:     body.DurationMS,
		SizeBytes:      body.SizeBytes,
		ChecksumSHA256: body.ChecksumSHA256,
		Policy:         body.Policy,
		ExpiresAt:      body.ExpiresAt,
	}
	actor := userID + ":device:" + deviceID
	request, err := voiceMailRequest(actor, "voice_mail.create", body.IdempotencyKey, create)
	if err != nil {
		voiceMailError(w, err)
		return
	}
	item, err := s.voiceMail.CreateUpload(r.Context(), request, create)
	if err != nil {
		voiceMailError(w, err)
		return
	}
	writeVoiceMailJSON(w, http.StatusCreated, item)
}

func (s *Server) handleVoiceMailMediaPut(w http.ResponseWriter, r *http.Request) {
	userID, deviceID, ok := s.voiceMailIdentity(w, r)
	if !ok {
		return
	}
	body := http.MaxBytesReader(w, r.Body, voicemail.MaxSize+1)
	if err := s.voiceMail.PutMedia(r.Context(), userID, deviceID, r.PathValue("id"), body); err != nil {
		voiceMailError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleVoiceMailComplete(w http.ResponseWriter, r *http.Request) {
	userID, deviceID, ok := s.voiceMailIdentity(w, r)
	if !ok {
		return
	}
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := decodeVoiceMailJSON(w, r, &body); err != nil {
		return
	}
	actor := userID + ":device:" + deviceID
	request, err := voiceMailRequest(actor, "voice_mail.complete", body.IdempotencyKey, map[string]string{"id": r.PathValue("id")})
	if err != nil {
		voiceMailError(w, err)
		return
	}
	item, err := s.voiceMail.CompleteUpload(r.Context(), request, userID, deviceID, r.PathValue("id"))
	if err != nil {
		voiceMailError(w, err)
		return
	}
	writeVoiceMailJSON(w, http.StatusOK, item)
}

func (s *Server) handleVoiceMailList(w http.ResponseWriter, r *http.Request) {
	userID, deviceID, ok := s.voiceMailIdentity(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := s.voiceMail.ListUnread(r.Context(), userID, deviceID, limit)
	if err != nil {
		voiceMailError(w, err)
		return
	}
	writeVoiceMailJSON(w, http.StatusOK, map[string]any{"items": items})
}

type voiceMailClaimRequest struct {
	PlaybackID     string `json:"playback_id"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (s *Server) handleVoiceMailClaim(w http.ResponseWriter, r *http.Request) {
	userID, deviceID, ok := s.voiceMailIdentity(w, r)
	if !ok {
		return
	}
	var body voiceMailClaimRequest
	if err := decodeVoiceMailJSON(w, r, &body); err != nil {
		return
	}
	semantic := map[string]string{"id": r.PathValue("id"), "playback_id": body.PlaybackID, "device_id": deviceID}
	request, err := voiceMailRequest(userID, "voice_mail.claim", body.IdempotencyKey, semantic)
	if err != nil {
		voiceMailError(w, err)
		return
	}
	item, err := s.voiceMail.Claim(r.Context(), request, userID, deviceID, r.PathValue("id"), body.PlaybackID)
	if err != nil {
		voiceMailError(w, err)
		return
	}
	writeVoiceMailJSON(w, http.StatusOK, item)
}

func (s *Server) handleVoiceMailMediaGet(w http.ResponseWriter, r *http.Request) {
	userID, deviceID, ok := s.voiceMailIdentity(w, r)
	if !ok {
		return
	}
	reader, err := s.voiceMail.OpenMedia(r.Context(), userID, deviceID, r.PathValue("id"), r.URL.Query().Get("playback_id"))
	if err != nil {
		voiceMailError(w, err)
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Type", "audio/ogg")
	if _, err := io.Copy(w, reader); err != nil {
		s.logger.Warn("stream voice mail media", "error", err)
	}
}

type voiceMailPlaybackRequest struct {
	PlaybackID     string                  `json:"playback_id"`
	Result         protocol.PlaybackResult `json:"result"`
	IdempotencyKey string                  `json:"idempotency_key"`
}

func (s *Server) handleVoiceMailPlayback(w http.ResponseWriter, r *http.Request) {
	userID, deviceID, ok := s.voiceMailIdentity(w, r)
	if !ok {
		return
	}
	var body voiceMailPlaybackRequest
	if err := decodeVoiceMailJSON(w, r, &body); err != nil {
		return
	}
	if body.Result != protocol.PlaybackSucceeded && body.Result != protocol.PlaybackFailed {
		http.Error(w, "invalid result", http.StatusBadRequest)
		return
	}
	semantic := map[string]any{"id": r.PathValue("id"), "playback_id": body.PlaybackID, "result": body.Result, "device_id": deviceID}
	request, err := voiceMailRequest(userID, "voice_mail.playback", body.IdempotencyKey, semantic)
	if err != nil {
		voiceMailError(w, err)
		return
	}
	item, err := s.voiceMail.Playback(r.Context(), request, userID, deviceID, r.PathValue("id"), body.PlaybackID, body.Result == protocol.PlaybackSucceeded)
	if err != nil {
		voiceMailError(w, err)
		return
	}
	writeVoiceMailJSON(w, http.StatusOK, item)
}

func (s *Server) handleVoiceMailDelete(w http.ResponseWriter, r *http.Request) {
	userID, _, ok := s.voiceMailIdentity(w, r)
	if !ok {
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	request, err := voiceMailRequest(userID, "voice_mail.delete", key, map[string]string{"id": r.PathValue("id")})
	if err != nil {
		voiceMailError(w, err)
		return
	}
	item, err := s.voiceMail.Delete(r.Context(), request, userID, r.PathValue("id"))
	if err != nil {
		voiceMailError(w, err)
		return
	}
	writeVoiceMailJSON(w, http.StatusOK, item)
}

func decodeVoiceMailJSON(w http.ResponseWriter, r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return fmt.Errorf("request must contain one JSON value")
	}
	return nil
}
func writeVoiceMailJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func voiceMailError(w http.ResponseWriter, err error) {
	status, message := http.StatusBadRequest, "voice mail request rejected"
	if idempotency.IsConflict(err) {
		status = http.StatusConflict
		message = idempotency.ConflictCode
	} else if errors.Is(err, voicemail.ErrUnauthorized) {
		status = http.StatusForbidden
		message = "forbidden"
	} else if errors.Is(err, voicemail.ErrRelationshipNotFound) || errors.Is(err, voicemail.ErrItemNotFound) {
		status = http.StatusNotFound
		message = "not found"
	} else if errors.Is(err, voicemail.ErrRelationshipRevoked) {
		status = http.StatusConflict
		message = "relationship revoked"
	}
	http.Error(w, message, status)
}

func (s *Server) HandleEvent(ctx context.Context, event events.Event) error {
	if event.Type != "voice_mail.available" && event.Type != "voice_mail.consumed" && event.Type != "voice_mail.expired" {
		return nil
	}
	var payload struct {
		VoiceMailID       string                   `json:"voice_mail_id"`
		RecipientDeviceID string                   `json:"recipient_device_id"`
		FromDeviceID      string                   `json:"from_device_id"`
		MediaFormat       string                   `json:"media_format"`
		DurationMS        int64                    `json:"duration_ms"`
		SizeBytes         int64                    `json:"size_bytes"`
		ChecksumSHA256    string                   `json:"checksum_sha256"`
		ExpiresAt         time.Time                `json:"expires_at"`
		Policy            protocol.VoiceMailPolicy `json:"policy"`
	}
	if err := json.Unmarshal(event.Data, &payload); err != nil {
		return fmt.Errorf("decode voice mail outbox event: %w", err)
	}
	for _, target := range s.hub.targets(event.UserID, payload.RecipientDeviceID) {
		metadata := protocol.Metadata{IdempotencyKey: event.ID, OccurredAt: event.Time.UTC().Format(time.RFC3339Nano)}
		var err error
		switch event.Type {
		case "voice_mail.available":
			message := protocol.VoiceMailAvailable{VoiceMailID: payload.VoiceMailID, FromDeviceID: payload.FromDeviceID, MediaFormat: payload.MediaFormat, DurationMS: payload.DurationMS, SizeBytes: payload.SizeBytes, ChecksumSHA256: payload.ChecksumSHA256, ExpiresAt: payload.ExpiresAt, Policy: payload.Policy}
			if validateErr := message.Validate(); validateErr != nil {
				return validateErr
			}
			err = target.sendJSONMeta(ctx, protocol.VoiceMailAvailableType, metadata, message)
		case "voice_mail.consumed":
			message := protocol.VoiceMailConsumed{VoiceMailID: payload.VoiceMailID}
			if validateErr := message.Validate(); validateErr != nil {
				return validateErr
			}
			err = target.sendJSONMeta(ctx, protocol.VoiceMailConsumedType, metadata, message)
		case "voice_mail.expired":
			message := protocol.VoiceMailExpired{VoiceMailID: payload.VoiceMailID}
			if validateErr := message.Validate(); validateErr != nil {
				return validateErr
			}
			err = target.sendJSONMeta(ctx, protocol.VoiceMailExpiredType, metadata, message)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
