package server

import (
	"context"
	"fmt"
	"strings"

	"companion-server/internal/protocol"
)

func (s *session) sendTurnUIState(ctx context.Context, current *turn, emotion protocol.UIEmotion, toolName string) error {
	payload := protocol.UIStatePayload{Emotion: emotion, ToolName: strings.TrimSpace(toolName)}
	if err := protocol.ValidateUIState(payload); err != nil {
		return fmt.Errorf("invalid ui state: %w", err)
	}
	return s.sendTurnJSON(ctx, current, protocol.UIStateType, payload)
}
