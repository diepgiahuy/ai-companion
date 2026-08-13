package tools

import (
	"context"
	"fmt"
	"strings"

	"companion-server/internal/idempotency"
	"companion-server/internal/pipeline"
)

func durableMutationRequest(ctx context.Context, operation, key string, payload any) (idempotency.Request, error) {
	turn, ok := pipeline.CurrentTurn(ctx)
	if !ok {
		return idempotency.Request{}, fmt.Errorf("authenticated turn context is required for %s", operation)
	}
	userID := strings.TrimSpace(turn.UserID)
	if userID == "" {
		return idempotency.Request{}, fmt.Errorf("authenticated user is required for %s", operation)
	}
	actorHash, err := idempotency.HashValue([]string{strings.TrimSpace(turn.TenantID), userID})
	if err != nil {
		return idempotency.Request{}, err
	}
	requestHash, err := idempotency.HashValue(payload)
	if err != nil {
		return idempotency.Request{}, err
	}
	request := idempotency.Request{Actor: "actor:" + actorHash, Operation: strings.TrimSpace(operation), Key: strings.TrimSpace(key), RequestHash: requestHash}
	if err := request.Validate(); err != nil {
		return idempotency.Request{}, err
	}
	return request, nil
}
