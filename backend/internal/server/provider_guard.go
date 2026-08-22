package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"companion-server/internal/pipeline"
)

type providerStage string

const (
	providerStageASR   providerStage = "asr"
	providerStageAgent providerStage = "agent"
	providerStageTTS   providerStage = "tts"
)

var errProviderStageSaturated = errors.New("provider stage saturated by cancellation-ignoring call")

type providerTimeouts struct {
	ASR   time.Duration
	Agent time.Duration
	TTS   time.Duration
}

func defaultProviderTimeouts() providerTimeouts {
	return providerTimeouts{
		ASR:   30 * time.Second,
		Agent: 90 * time.Second,
		TTS:   45 * time.Second,
	}
}

type providerCallGuard struct {
	timeouts providerTimeouts
	asr      chan struct{}
	agent    chan struct{}
	tts      chan struct{}
}

func newProviderCallGuard(timeouts providerTimeouts) *providerCallGuard {
	defaults := defaultProviderTimeouts()
	if timeouts.ASR <= 0 {
		timeouts.ASR = defaults.ASR
	}
	if timeouts.Agent <= 0 {
		timeouts.Agent = defaults.Agent
	}
	if timeouts.TTS <= 0 {
		timeouts.TTS = defaults.TTS
	}
	return &providerCallGuard{
		timeouts: timeouts,
		asr:      make(chan struct{}, 1),
		agent:    make(chan struct{}, 1),
		tts:      make(chan struct{}, 1),
	}
}

func (g *providerCallGuard) stage(stage providerStage) (time.Duration, chan struct{}) {
	switch stage {
	case providerStageASR:
		return g.timeouts.ASR, g.asr
	case providerStageAgent:
		return g.timeouts.Agent, g.agent
	case providerStageTTS:
		return g.timeouts.TTS, g.tts
	default:
		panic(fmt.Sprintf("unknown provider stage %q", stage))
	}
}

type providerCallResult[T any] struct {
	value T
	err   error
}

func guardedProviderCall[T any](ctx context.Context, guard *providerCallGuard, stage providerStage, fn func(context.Context) (T, error)) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if guard == nil {
		return fn(ctx)
	}
	timeout, slot := guard.stage(stage)
	select {
	case slot <- struct{}{}:
	default:
		return zero, fmt.Errorf("%w: %s", errProviderStageSaturated, stage)
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	resultCh := make(chan providerCallResult[T], 1)
	go func() {
		defer func() { <-slot }()
		defer func() {
			if recovered := recover(); recovered != nil {
				resultCh <- providerCallResult[T]{err: fmt.Errorf("%s provider panic: %v", stage, recovered)}
			}
		}()
		value, err := fn(callCtx)
		resultCh <- providerCallResult[T]{value: value, err: err}
	}()

	select {
	case result := <-resultCh:
		cancel()
		return result.value, result.err
	case <-callCtx.Done():
		err := callCtx.Err()
		cancel()
		return zero, fmt.Errorf("%s provider call exceeded lifecycle budget: %w", stage, err)
	}
}

func guardedProviderDo(ctx context.Context, guard *providerCallGuard, stage providerStage, fn func(context.Context) error) error {
	_, err := guardedProviderCall(ctx, guard, stage, func(callCtx context.Context) (struct{}, error) {
		return struct{}{}, fn(callCtx)
	})
	return err
}

type guardedASR struct {
	inner pipeline.ASR
	guard *providerCallGuard
}

func (g guardedASR) Transcribe(ctx context.Context, pcm []byte) (string, error) {
	return guardedProviderCall(ctx, g.guard, providerStageASR, func(callCtx context.Context) (string, error) {
		return g.inner.Transcribe(callCtx, pcm)
	})
}

type guardedAgentBase struct {
	inner pipeline.Agent
	guard *providerCallGuard
}

func (g guardedAgentBase) Respond(ctx context.Context, turnID, transcript string) (string, error) {
	return guardedProviderCall(ctx, g.guard, providerStageAgent, func(callCtx context.Context) (string, error) {
		return g.inner.Respond(callCtx, turnID, transcript)
	})
}

type guardedRichAgent struct {
	guardedAgentBase
	rich pipeline.RichAgent
}

func (g guardedRichAgent) RespondRich(ctx context.Context, turnID, transcript string) (pipeline.AgentResult, error) {
	return guardedProviderCall(ctx, g.guard, providerStageAgent, func(callCtx context.Context) (pipeline.AgentResult, error) {
		return g.rich.RespondRich(callCtx, turnID, transcript)
	})
}

type guardedStreamingAgent struct {
	guardedAgentBase
	streaming pipeline.StreamingAgent
}

func (g guardedStreamingAgent) Stream(ctx context.Context, turnID, transcript string, emit func(pipeline.AgentStreamEvent) error) error {
	return guardedProviderDo(ctx, g.guard, providerStageAgent, func(callCtx context.Context) error {
		return g.streaming.Stream(callCtx, turnID, transcript, func(event pipeline.AgentStreamEvent) error {
			if err := callCtx.Err(); err != nil {
				return err
			}
			return emit(event)
		})
	})
}

type guardedStreamingRichAgent struct {
	guardedAgentBase
	rich      pipeline.RichAgent
	streaming pipeline.StreamingAgent
}

func (g guardedStreamingRichAgent) RespondRich(ctx context.Context, turnID, transcript string) (pipeline.AgentResult, error) {
	return guardedProviderCall(ctx, g.guard, providerStageAgent, func(callCtx context.Context) (pipeline.AgentResult, error) {
		return g.rich.RespondRich(callCtx, turnID, transcript)
	})
}

func (g guardedStreamingRichAgent) Stream(ctx context.Context, turnID, transcript string, emit func(pipeline.AgentStreamEvent) error) error {
	return guardedProviderDo(ctx, g.guard, providerStageAgent, func(callCtx context.Context) error {
		return g.streaming.Stream(callCtx, turnID, transcript, func(event pipeline.AgentStreamEvent) error {
			if err := callCtx.Err(); err != nil {
				return err
			}
			return emit(event)
		})
	})
}

type guardedTTS struct {
	inner pipeline.TTS
	guard *providerCallGuard
}

func (g guardedTTS) Synthesize(ctx context.Context, text string, emit func([]byte) error) error {
	return guardedProviderDo(ctx, g.guard, providerStageTTS, func(callCtx context.Context) error {
		return g.inner.Synthesize(callCtx, text, func(frame []byte) error {
			if err := callCtx.Err(); err != nil {
				return err
			}
			return emit(frame)
		})
	})
}

type batchAgentSpeechProvider interface {
	RequiresBatchAgent() bool
}

func speechRequiresBatchAgent(components pipeline.Components) bool {
	for _, component := range []any{components.ASR, components.TTS} {
		provider, ok := component.(batchAgentSpeechProvider)
		if ok && provider.RequiresBatchAgent() {
			return true
		}
	}
	return false
}

func guardProviderComponents(components pipeline.Components, guard *providerCallGuard) pipeline.Components {
	requiresBatchAgent := speechRequiresBatchAgent(components)
	if components.ASR != nil {
		components.ASR = guardedASR{inner: components.ASR, guard: guard}
	}
	if components.TTS != nil {
		components.TTS = guardedTTS{inner: components.TTS, guard: guard}
	}
	if components.Agent == nil {
		return components
	}

	base := guardedAgentBase{inner: components.Agent, guard: guard}
	streaming, hasStreaming := components.Agent.(pipeline.StreamingAgent)
	if requiresBatchAgent {
		hasStreaming = false
	}
	rich, hasRich := components.Agent.(pipeline.RichAgent)
	switch {
	case hasStreaming && hasRich:
		components.Agent = guardedStreamingRichAgent{guardedAgentBase: base, rich: rich, streaming: streaming}
	case hasStreaming:
		components.Agent = guardedStreamingAgent{guardedAgentBase: base, streaming: streaming}
	case hasRich:
		components.Agent = guardedRichAgent{guardedAgentBase: base, rich: rich}
	default:
		components.Agent = base
	}
	return components
}
