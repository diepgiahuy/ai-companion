//go:build adk

package server

import (
	"context"

	conversationctx "companion-server/internal/conversation"
	"companion-server/internal/store"
)

// voiceEvidenceConversationStore adapts the isolated SQLite test store to the
// durable conversation contract used by the canonical ADK runtime. It exists
// only in tests; SQLite remains outside the product runtime path.
type voiceEvidenceConversationStore struct {
	data *store.Store
}

func (s voiceEvidenceConversationStore) Append(ctx context.Context, turnKey string, scope conversationctx.Scope, role, content string) error {
	return s.data.SaveConversationMessageScoped(ctx, turnKey, scope.UserID, scope.ThreadID, role, content)
}

func (s voiceEvidenceConversationStore) Recent(ctx context.Context, scope conversationctx.Scope, limit int) ([]conversationctx.Message, error) {
	history, err := s.data.ConversationHistoryScoped(ctx, scope.UserID, scope.ThreadID, limit)
	if err != nil {
		return nil, err
	}
	messages := make([]conversationctx.Message, 0, len(history))
	for _, item := range history {
		messages = append(messages, conversationctx.Message{
			Role:      item.Role,
			Content:   item.Content,
			CreatedAt: item.CreatedAt,
		})
	}
	return messages, nil
}

func (s voiceEvidenceConversationStore) Clear(ctx context.Context, scope conversationctx.Scope) error {
	return s.data.DeleteConversationThread(ctx, scope.UserID, scope.ThreadID)
}
