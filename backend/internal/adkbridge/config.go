package adkbridge

import (
	"context"
	"errors"
	"net/http"

	"companion-server/internal/capability"
	conversationctx "companion-server/internal/conversation"
	usagepkg "companion-server/internal/usage"
)

var ErrNotBuilt = errors.New("ADK runtime is not included in this build; rebuild with -tags=adk")

const (
	ModelProtocolResponses = "responses"
	ModelProtocolChatCompletions = "chat_completions"
)

type UsageGuard interface {
	Check(context.Context, string) error
}

type Config struct {
	AppName       string
	ModelName     string
	ModelProtocol string
	ProviderToolAliases bool
	BaseURL       string
	APIKey        string
	Instruction   string
	PromptVersion string
	HTTPClient    *http.Client
	Tools         *capability.ToolRegistry
	Conversation  *conversationctx.Service
	HistoryLimit  int
	UsageGuard    UsageGuard
	UsageMeter    usagepkg.Meter
}
