package adkbridge

import (
	"context"
	"errors"
	"net/http"

	"companion-server/internal/capability"
	usagepkg "companion-server/internal/usage"
)

// ErrNotBuilt is returned when the binary was compiled without the adk build
// tag. Keeping the legacy path buildable is intentional: ADK is introduced as
// a reversible migration until its parity gates pass.
var ErrNotBuilt = errors.New("ADK runtime is not included in this build; rebuild with -tags=adk")

// UsageGuard is intentionally framework-neutral so quota policy stays owned by
// Companion instead of ADK/provider code.
type UsageGuard interface {
	Check(context.Context, string) error
}

// Config contains only Companion-owned types and standard-library types. ADK
// types stay inside the adapter implementation so product/domain packages do
// not become coupled to a framework dependency.
type Config struct {
	AppName       string
	ModelName     string
	BaseURL       string
	APIKey        string
	Instruction   string
	PromptVersion string
	HTTPClient    *http.Client
	Tools         *capability.ToolRegistry
	UsageGuard    UsageGuard
	UsageMeter    usagepkg.Meter
}
