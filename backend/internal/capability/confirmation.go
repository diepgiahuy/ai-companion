package capability

import (
	"context"
	"time"
)

// ConfirmationIntent is created by the policy layer from the exact destructive
// tool invocation. ArgumentsHash is server-internal binding material; transport
// adapters must not expose raw arbitrary tool arguments or treat device output
// as authority to change this intent.
type ConfirmationIntent struct {
	ToolName      string
	Description   string
	ArgumentsHash string
	ExpiresAt     time.Time
}

// ConfirmationRequester asks the already-authenticated user/device session for
// explicit local approval. Implementations must fail closed on timeout,
// disconnect, stale turn/generation, replay, or transport failure.
type ConfirmationRequester interface {
	RequestConfirmation(context.Context, ConfirmationIntent) (bool, error)
}

type confirmationRequesterKey struct{}

func WithConfirmationRequester(ctx context.Context, requester ConfirmationRequester) context.Context {
	if requester == nil {
		return ctx
	}
	return context.WithValue(ctx, confirmationRequesterKey{}, requester)
}

func ConfirmationRequesterFromContext(ctx context.Context) (ConfirmationRequester, bool) {
	requester, ok := ctx.Value(confirmationRequesterKey{}).(ConfirmationRequester)
	return requester, ok && requester != nil
}
