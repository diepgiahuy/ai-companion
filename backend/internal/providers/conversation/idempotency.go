package conversation

import (
	"context"

	conversationctx "companion-server/internal/conversation"
	"companion-server/internal/idempotency"
)

func (s *SQLite) ClearMutation(ctx context.Context, request idempotency.Request, scope conversationctx.Scope) (bool, error) {
	return s.data.ClearConversationMutation(ctx, request, scope.UserID, scope.ThreadID)
}
