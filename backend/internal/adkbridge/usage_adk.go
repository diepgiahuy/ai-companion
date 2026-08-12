//go:build adk

package adkbridge

import (
	"context"
	"iter"
	"strings"

	"google.golang.org/adk/v2/model"

	"companion-server/internal/pipeline"
	usagepkg "companion-server/internal/usage"
)

// meteredLLM keeps quota and usage accounting outside the ADK framework. This
// preserves Companion's existing cost-control boundary while allowing the model
// implementation to be replaced independently.
type meteredLLM struct {
	inner         model.LLM
	modelName     string
	promptVersion string
	guard         UsageGuard
	meter         usagepkg.Meter
}

func (m *meteredLLM) Name() string { return m.inner.Name() }

func (m *meteredLLM) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		turn, _ := pipeline.CurrentTurn(ctx)
		userID := strings.TrimSpace(turn.UserID)
		if userID == "" {
			userID = strings.TrimSpace(turn.DeviceID)
		}
		if m.guard != nil {
			if err := m.guard.Check(ctx, userID); err != nil {
				yield(nil, err)
				return
			}
		}

		var usage *usagepkg.Record
		for response, err := range m.inner.GenerateContent(ctx, req, stream) {
			if response != nil && response.UsageMetadata != nil && response.UsageMetadata.TotalTokenCount > 0 {
				modelName := m.modelName
				if req != nil && strings.TrimSpace(req.Model) != "" {
					modelName = strings.TrimSpace(req.Model)
				}
				u := response.UsageMetadata
				usage = &usagepkg.Record{
					Provider:         "adk-openai-compatible",
					Model:            modelName,
					PromptVersion:    m.promptVersion,
					PromptTokens:     int(u.PromptTokenCount),
					CompletionTokens: int(u.CandidatesTokenCount),
					TotalTokens:      int(u.TotalTokenCount),
					UserID:           turn.UserID,
					DeviceID:         turn.DeviceID,
				}
			}
			if !yield(response, err) {
				break
			}
			if err != nil {
				break
			}
		}
		// Streaming providers may repeat cumulative usage metadata on multiple
		// chunks. Record the latest snapshot exactly once per model invocation.
		if usage != nil && m.meter != nil {
			m.meter.RecordUsage(ctx, *usage)
		}
	}
}
