package conversation

import (
	conversationctx "companion-server/internal/conversation"
	"companion-server/internal/store"
	"context"
)

type SQLite struct{ data *store.Store }

func NewSQLite(data *store.Store) *SQLite { return &SQLite{data: data} }
func (s *SQLite) Append(ctx context.Context, turnKey string, scope conversationctx.Scope, role, content string) error {
	return s.data.SaveConversationMessageScoped(ctx, turnKey, scope.UserID, scope.ThreadID, role, content)
}
func (s *SQLite) Recent(ctx context.Context, scope conversationctx.Scope, limit int) ([]conversationctx.Message, error) {
	rows, err := s.data.ConversationHistoryScoped(ctx, scope.UserID, scope.ThreadID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]conversationctx.Message, 0, len(rows))
	for _, row := range rows {
		out = append(out, conversationctx.Message{Role: row.Role, Content: row.Content, CreatedAt: row.CreatedAt})
	}
	return out, nil
}

func (s *SQLite) Clear(ctx context.Context, scope conversationctx.Scope) error {
	return s.data.DeleteConversationThread(ctx, scope.UserID, scope.ThreadID)
}
