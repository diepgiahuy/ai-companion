package server

import (
	"context"
	"fmt"
	"strings"

	"companion-server/internal/protocol"
)

func (s *session) sendTurnUIState(ctx context.Context, current *turn, emotion protocol.UIEmotion, toolName string) error {
	message := protocol.Message{
		Type:      "ui_state",
		SessionID: s.id,
		TurnID:    current.id,
		Emotion:   emotion,
		ToolName:  strings.TrimSpace(toolName),
	}
	if err := protocol.ValidateUIState(message); err != nil {
		return fmt.Errorf("invalid ui state: %w", err)
	}
	return s.sendTurnJSON(ctx, current, message)
}
