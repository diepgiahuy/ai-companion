package pgstore

import (
	"context"
	"fmt"
	"strings"
	"time"

	conversationctx "companion-server/internal/conversation"
)

func conversationScope(scope conversationctx.Scope) (string, string) {
	userID := owner(scope.UserID)
	threadID := strings.TrimSpace(scope.ThreadID)
	if threadID == "" { threadID = "default" }
	return userID, threadID
}

func (s *Store) Append(ctx context.Context, turnKey string, scope conversationctx.Scope, role, content string) error {
	turnKey = strings.TrimSpace(turnKey)
	role = strings.TrimSpace(role)
	content = strings.TrimSpace(content)
	if turnKey == "" || content == "" || (role != "user" && role != "assistant") {
		return fmt.Errorf("conversation append requires turn key, supported role and content")
	}
	userID, threadID := conversationScope(scope)
	_, err := s.pool.Exec(ctx, `INSERT INTO conversation_messages(turn_key,user_id,thread_id,role,content,created_at)
		VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(turn_key,role) DO NOTHING`, turnKey,userID,threadID,role,content,time.Now().UTC())
	return err
}

func (s *Store) Recent(ctx context.Context, scope conversationctx.Scope, limit int) ([]conversationctx.Message, error) {
	userID, threadID := conversationScope(scope)
	rows, err := s.pool.Query(ctx, `SELECT role,content,created_at FROM (
		SELECT id,role,content,created_at FROM conversation_messages WHERE user_id=$1 AND thread_id=$2 ORDER BY id DESC LIMIT $3
	) recent ORDER BY id ASC`, userID,threadID,boundedLimit(limit))
	if err != nil { return nil, err }
	defer rows.Close()
	var out []conversationctx.Message
	for rows.Next() {
		var x conversationctx.Message
		if err := rows.Scan(&x.Role,&x.Content,&x.CreatedAt); err != nil { return nil, err }
		x.CreatedAt=x.CreatedAt.UTC();out=append(out,x)
	}
	return out,rows.Err()
}

func (s *Store) Clear(ctx context.Context, scope conversationctx.Scope) error {
	userID, threadID := conversationScope(scope)
	_, err := s.pool.Exec(ctx, `DELETE FROM conversation_messages WHERE user_id=$1 AND thread_id=$2`, userID,threadID)
	return err
}

var _ conversationctx.Store = (*Store)(nil)
