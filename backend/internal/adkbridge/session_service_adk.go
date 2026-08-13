//go:build adk

package adkbridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	conversationpkg "companion-server/internal/conversation"
)

const durableHistoryLimit = 32

// ConversationHistory is the Companion-owned durable conversation seam used to
// recover an ADK working session after process restart. ADK event/state storage
// remains execution-framework state; authoritative domain state continues to
// live behind ToolRegistry/repositories.
type ConversationHistory interface {
	Append(context.Context, string, conversationpkg.Scope, string, string) error
	Recent(context.Context, conversationpkg.Scope, int) ([]conversationpkg.Message, error)
}

type companionSessionService struct {
	inner   session.Service
	history ConversationHistory

	mu       sync.RWMutex
	bindings map[string]conversationpkg.Scope
}

func newCompanionSessionService(history ConversationHistory) (*companionSessionService, error) {
	if history == nil {
		return nil, fmt.Errorf("Companion conversation history is required for ADK session recovery")
	}
	return &companionSessionService{
		inner:    session.InMemoryService(),
		history:  history,
		bindings: make(map[string]conversationpkg.Scope),
	}, nil
}

func sessionBindingKey(appName, userID, sessionID string) string {
	return strings.TrimSpace(appName) + "\x00" + strings.TrimSpace(userID) + "\x00" + strings.TrimSpace(sessionID)
}

func (s *companionSessionService) Bind(appName, userID, sessionID string, scope conversationpkg.Scope) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.bindings[sessionBindingKey(appName, userID, sessionID)] = scope
	s.mu.Unlock()
}

func (s *companionSessionService) binding(appName, userID, sessionID string) (conversationpkg.Scope, bool) {
	s.mu.RLock()
	scope, ok := s.bindings[sessionBindingKey(appName, userID, sessionID)]
	s.mu.RUnlock()
	return scope, ok
}

func (s *companionSessionService) Create(ctx context.Context, req *session.CreateRequest) (*session.CreateResponse, error) {
	resp, err := s.inner.Create(ctx, req)
	if err != nil {
		return nil, err
	}
	scope, ok := s.binding(req.AppName, req.UserID, resp.Session.ID())
	if !ok {
		return resp, nil
	}
	messages, err := s.history.Recent(ctx, scope, durableHistoryLimit)
	if err != nil {
		_ = s.inner.Delete(ctx, &session.DeleteRequest{AppName: req.AppName, UserID: req.UserID, SessionID: resp.Session.ID()})
		return nil, fmt.Errorf("load Companion conversation history: %w", err)
	}
	for _, message := range messages {
		text := strings.TrimSpace(message.Content)
		if text == "" {
			continue
		}
		role, author := "model", "companion"
		if strings.EqualFold(strings.TrimSpace(message.Role), "user") {
			role, author = "user", "user"
		}
		event := session.NewEvent(ctx, "companion-history-bootstrap")
		event.Author = author
		event.LLMResponse = model.LLMResponse{Content: &genai.Content{Role: role, Parts: []*genai.Part{{Text: text}}}}
		if !message.CreatedAt.IsZero() {
			event.Timestamp = message.CreatedAt
		}
		// Append directly to the working service so recovery does not write the
		// same durable history back into Companion storage.
		if err := s.inner.AppendEvent(ctx, resp.Session, event); err != nil {
			_ = s.inner.Delete(ctx, &session.DeleteRequest{AppName: req.AppName, UserID: req.UserID, SessionID: resp.Session.ID()})
			return nil, fmt.Errorf("bootstrap ADK working session: %w", err)
		}
	}
	if len(messages) == 0 {
		return resp, nil
	}
	current, err := s.inner.Get(ctx, &session.GetRequest{AppName: req.AppName, UserID: req.UserID, SessionID: resp.Session.ID()})
	if err != nil {
		return nil, err
	}
	return &session.CreateResponse{Session: current.Session}, nil
}

func (s *companionSessionService) Get(ctx context.Context, req *session.GetRequest) (*session.GetResponse, error) {
	return s.inner.Get(ctx, req)
}

func (s *companionSessionService) List(ctx context.Context, req *session.ListRequest) (*session.ListResponse, error) {
	return s.inner.List(ctx, req)
}

func (s *companionSessionService) Delete(ctx context.Context, req *session.DeleteRequest) error {
	return s.inner.Delete(ctx, req)
}

func (s *companionSessionService) AppendEvent(ctx context.Context, current session.Session, event *session.Event) error {
	if err := s.inner.AppendEvent(ctx, current, event); err != nil {
		return err
	}
	if event == nil || event.Content == nil {
		return nil
	}
	text := strings.TrimSpace(contentText(event.Content))
	if text == "" {
		return nil
	}
	role := ""
	if strings.EqualFold(strings.TrimSpace(event.Content.Role), "user") || strings.EqualFold(strings.TrimSpace(event.Author), "user") {
		role = "user"
	} else if event.IsFinalResponse() {
		role = "assistant"
	}
	if role == "" {
		return nil
	}
	scope, ok := s.binding(current.AppName(), current.UserID(), current.ID())
	if !ok {
		return fmt.Errorf("ADK session %q has no Companion conversation binding", current.ID())
	}
	key := strings.TrimSpace(event.ID)
	if key == "" {
		sum := sha256.Sum256([]byte(current.AppName() + "\x00" + current.UserID() + "\x00" + current.ID() + "\x00" + event.InvocationID + "\x00" + role + "\x00" + text))
		key = hex.EncodeToString(sum[:])
	}
	if err := s.history.Append(ctx, "adk-event:"+key, scope, role, text); err != nil {
		return fmt.Errorf("persist Companion conversation event: %w", err)
	}
	return nil
}
