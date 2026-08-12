package server

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"companion-server/internal/domain"
	"companion-server/internal/protocol"
)

type sessionHub struct {
	mu       sync.RWMutex
	byDevice map[string]map[*session]struct{}
}

func newSessionHub() *sessionHub {
	return &sessionHub{byDevice: make(map[string]map[*session]struct{})}
}

func (h *sessionHub) register(deviceID string, s *session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set := h.byDevice[deviceID]
	if set == nil {
		set = make(map[*session]struct{})
		h.byDevice[deviceID] = set
	}
	set[s] = struct{}{}
}

func (h *sessionHub) unregister(deviceID string, s *session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set := h.byDevice[deviceID]
	delete(set, s)
	if len(set) == 0 {
		delete(h.byDevice, deviceID)
	}
}

type hubIdentity struct {
	UserID   string
	DeviceID string
	TenantID string
	Plan     string
}

func (h *sessionHub) identities() []hubIdentity {
	h.mu.RLock()
	defer h.mu.RUnlock()
	seen := map[hubIdentity]bool{}
	var result []hubIdentity
	for deviceID, sessions := range h.byDevice {
		for session := range sessions {
			identity := hubIdentity{UserID: session.userID, DeviceID: deviceID, TenantID: session.tenantID, Plan: session.plan}
			if !seen[identity] {
				seen[identity] = true
				result = append(result, identity)
			}
		}
	}
	return result
}

func (h *sessionHub) targets(userID, deviceID string) []*session {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var result []*session
	appendIfOwned := func(session *session) {
		if userID == "" || session.userID == userID {
			result = append(result, session)
		}
	}
	if deviceID == "" {
		for _, sessions := range h.byDevice {
			for session := range sessions {
				appendIfOwned(session)
			}
		}
		return result
	}
	for session := range h.byDevice[deviceID] {
		appendIfOwned(session)
	}
	return result
}

func (h *sessionHub) pushAlarm(ctx context.Context, reminder domain.ScheduledItem) int {
	sent := 0
	for _, target := range h.targets(reminder.UserID, reminder.DeviceID) {
		if err := target.sendJSON(ctx, protocol.Message{
			Type:    "alarm",
			ID:      fmt.Sprintf("reminder-%d", reminder.ID),
			Message: oledText(reminder.Title),
			FireAt:  reminder.FireAt.UTC().Format(time.RFC3339),
		}); err == nil {
			sent++
		}
	}
	return sent
}

func (h *sessionHub) pushSchedule(ctx context.Context, userID, deviceID, summary, fireAt string) int {
	sent := 0
	for _, target := range h.targets(userID, deviceID) {
		if err := target.sendJSON(ctx, protocol.Message{
			Type: "schedule", Message: oledText(summary), FireAt: fireAt,
		}); err == nil {
			sent++
		}
	}
	return sent
}

// oledText degrades Vietnamese diacritics to ASCII because the current 5x7 OLED
// font is ASCII-only. Keep the original title in SQLite for voice/query use.
func oledText(value string) string {
	replacer := strings.NewReplacer(
		"à", "a", "á", "a", "ạ", "a", "ả", "a", "ã", "a", "â", "a", "ầ", "a", "ấ", "a", "ậ", "a", "ẩ", "a", "ẫ", "a", "ă", "a", "ằ", "a", "ắ", "a", "ặ", "a", "ẳ", "a", "ẵ", "a",
		"è", "e", "é", "e", "ẹ", "e", "ẻ", "e", "ẽ", "e", "ê", "e", "ề", "e", "ế", "e", "ệ", "e", "ể", "e", "ễ", "e",
		"ì", "i", "í", "i", "ị", "i", "ỉ", "i", "ĩ", "i",
		"ò", "o", "ó", "o", "ọ", "o", "ỏ", "o", "õ", "o", "ô", "o", "ồ", "o", "ố", "o", "ộ", "o", "ổ", "o", "ỗ", "o", "ơ", "o", "ờ", "o", "ớ", "o", "ợ", "o", "ở", "o", "ỡ", "o",
		"ù", "u", "ú", "u", "ụ", "u", "ủ", "u", "ũ", "u", "ư", "u", "ừ", "u", "ứ", "u", "ự", "u", "ử", "u", "ữ", "u",
		"ỳ", "y", "ý", "y", "ỵ", "y", "ỷ", "y", "ỹ", "y", "đ", "d",
		"À", "A", "Á", "A", "Ạ", "A", "Ả", "A", "Ã", "A", "Â", "A", "Ầ", "A", "Ấ", "A", "Ậ", "A", "Ẩ", "A", "Ẫ", "A", "Ă", "A", "Ằ", "A", "Ắ", "A", "Ặ", "A", "Ẳ", "A", "Ẵ", "A",
		"È", "E", "É", "E", "Ẹ", "E", "Ẻ", "E", "Ẽ", "E", "Ê", "E", "Ề", "E", "Ế", "E", "Ệ", "E", "Ể", "E", "Ễ", "E",
		"Ì", "I", "Í", "I", "Ị", "I", "Ỉ", "I", "Ĩ", "I",
		"Ò", "O", "Ó", "O", "Ọ", "O", "Ỏ", "O", "Õ", "O", "Ô", "O", "Ồ", "O", "Ố", "O", "Ộ", "O", "Ổ", "O", "Ỗ", "O", "Ơ", "O", "Ờ", "O", "Ớ", "O", "Ợ", "O", "Ở", "O", "Ỡ", "O",
		"Ù", "U", "Ú", "U", "Ụ", "U", "Ủ", "U", "Ũ", "U", "Ư", "U", "Ừ", "U", "Ứ", "U", "Ự", "U", "Ử", "U", "Ữ", "U",
		"Ỳ", "Y", "Ý", "Y", "Ỵ", "Y", "Ỷ", "Y", "Ỹ", "Y", "Đ", "D",
	)
	return replacer.Replace(value)
}

func (h *sessionHub) get(deviceID string) *session {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for s := range h.byDevice[deviceID] {
		return s
	}
	return nil
}
