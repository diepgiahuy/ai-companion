package speech

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"companion-server/internal/pipeline"
	"companion-server/internal/protocol"
)

const geminiDelegateAudioChunkBytes = 1280

type GeminiDelegatingBridge struct {
	provider NativeRealtimeProvider

	mu    sync.Mutex
	turns map[geminiDelegatingTurnKey]*geminiDelegatingTurn
}

type geminiDelegatingTurnKey struct {
	sessionID string
	turnID    string
}

type geminiDelegatingTurn struct {
	session NativeRealtimeSession
	call    NativeRealtimeToolCall
	done    chan struct{}
	once    sync.Once
}

func NewGeminiDelegatingBridge(provider NativeRealtimeProvider) (*GeminiDelegatingBridge, error) {
	if provider == nil {
		return nil, errors.New("Gemini delegation provider is required")
	}
	return &GeminiDelegatingBridge{
		provider: provider,
		turns:    make(map[geminiDelegatingTurnKey]*geminiDelegatingTurn),
	}, nil
}

func (b *GeminiDelegatingBridge) Transcribe(ctx context.Context, pcm []byte) (string, error) {
	key, err := geminiDelegatingKey(ctx)
	if err != nil {
		return "", err
	}
	if len(pcm) == 0 || len(pcm)%2 != 0 {
		return "", errors.New("Gemini delegation requires non-empty PCM16 input")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	b.mu.Lock()
	_, exists := b.turns[key]
	b.mu.Unlock()
	if exists {
		return "", fmt.Errorf("Gemini delegation turn %q is already active", key.turnID)
	}

	session, err := b.provider.Connect(ctx)
	if err != nil {
		return "", err
	}
	keepSession := false
	defer func() {
		if !keepSession {
			_ = session.Close()
		}
	}()

	for offset := 0; offset < len(pcm); offset += geminiDelegateAudioChunkBytes {
		end := offset + geminiDelegateAudioChunkBytes
		if end > len(pcm) {
			end = len(pcm)
		}
		if err := session.AppendAudio(ctx, pcm[offset:end]); err != nil {
			return "", err
		}
	}
	if err := session.CommitAudio(ctx); err != nil {
		return "", err
	}
	if err := session.CreateResponse(ctx); err != nil {
		return "", err
	}

	var transcript string
	var call *NativeRealtimeToolCall
	for strings.TrimSpace(transcript) == "" || call == nil {
		event, receiveErr := session.Receive(ctx)
		if receiveErr != nil {
			return "", receiveErr
		}
		if event.InputFinal && strings.TrimSpace(event.InputTranscript) != "" {
			transcript = strings.TrimSpace(event.InputTranscript)
		}
		if event.ToolCall != nil {
			copyCall := *event.ToolCall
			call = &copyCall
		}
		if len(event.AudioPCM) > 0 && call == nil {
			return "", errors.New("Gemini emitted audio before Companion delegation")
		}
		if event.ResponseDone && call == nil {
			return "", errors.New("Gemini completed without Companion delegation")
		}
	}

	state := &geminiDelegatingTurn{
		session: session,
		call:    *call,
		done:    make(chan struct{}),
	}
	b.mu.Lock()
	if _, exists := b.turns[key]; exists {
		b.mu.Unlock()
		return "", fmt.Errorf("Gemini delegation turn %q became active concurrently", key.turnID)
	}
	b.turns[key] = state
	b.mu.Unlock()
	keepSession = true
	go b.watchTurnCancellation(ctx, key, state)
	return transcript, nil
}

func (b *GeminiDelegatingBridge) Synthesize(ctx context.Context, text string, emit func([]byte) error) error {
	key, err := geminiDelegatingKey(ctx)
	if err != nil {
		return err
	}
	if emit == nil {
		return errors.New("Gemini delegation audio emitter is required")
	}

	b.mu.Lock()
	state := b.turns[key]
	b.mu.Unlock()
	if state == nil {
		if err := ctx.Err(); err != nil {
			return err
		}
		return fmt.Errorf("Gemini delegation turn %q has no pending provider session", key.turnID)
	}
	defer b.finishTurn(key, state)

	if err := state.session.ReturnToolResult(ctx, state.call.CallID, text); err != nil {
		return err
	}

	frameBytes := protocol.DownlinkSamplesPerFrame * 2
	buffer := make([]byte, 0, frameBytes*2)
	providerPCMBytes := 0
	for {
		event, receiveErr := state.session.Receive(ctx)
		if receiveErr != nil {
			if err := ctx.Err(); err != nil {
				return err
			}
			return receiveErr
		}
		if len(event.AudioPCM) > 0 {
			providerPCMBytes += len(event.AudioPCM)
			buffer = append(buffer, event.AudioPCM...)
			for len(buffer) >= frameBytes {
				frame := append([]byte(nil), buffer[:frameBytes]...)
				buffer = buffer[frameBytes:]
				if err := emit(frame); err != nil {
					return err
				}
			}
		}
		if event.ResponseDone {
			if event.ResponseStatus == "cancelled" {
				return context.Canceled
			}
			if providerPCMBytes == 0 {
				return errors.New("Gemini completed delegated response without native audio")
			}
			if len(buffer) > 0 {
				frame := make([]byte, frameBytes)
				copy(frame, buffer)
				if err := emit(frame); err != nil {
					return err
				}
			}
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

func (b *GeminiDelegatingBridge) watchTurnCancellation(ctx context.Context, key geminiDelegatingTurnKey, state *geminiDelegatingTurn) {
	select {
	case <-ctx.Done():
		b.cancelTurn(key, state)
	case <-state.done:
	}
}

func (b *GeminiDelegatingBridge) cancelTurn(key geminiDelegatingTurnKey, state *geminiDelegatingTurn) {
	if !b.detachTurn(key, state) {
		return
	}
	state.finish()
	cancelCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = state.session.CancelResponse(cancelCtx)
	cancel()
	_ = state.session.Close()
}

func (b *GeminiDelegatingBridge) finishTurn(key geminiDelegatingTurnKey, state *geminiDelegatingTurn) {
	if !b.detachTurn(key, state) {
		return
	}
	state.finish()
	_ = state.session.Close()
}

func (b *GeminiDelegatingBridge) detachTurn(key geminiDelegatingTurnKey, state *geminiDelegatingTurn) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.turns[key] != state {
		return false
	}
	delete(b.turns, key)
	return true
}

func (s *geminiDelegatingTurn) finish() {
	s.once.Do(func() { close(s.done) })
}

func geminiDelegatingKey(ctx context.Context) (geminiDelegatingTurnKey, error) {
	turn, ok := pipeline.CurrentTurn(ctx)
	if !ok {
		return geminiDelegatingTurnKey{}, errors.New("Gemini delegation requires Companion turn context")
	}
	sessionID := strings.TrimSpace(turn.SessionID)
	turnID := strings.TrimSpace(turn.TurnID)
	if sessionID == "" || turnID == "" {
		return geminiDelegatingTurnKey{}, errors.New("Gemini delegation requires session_id and turn_id")
	}
	return geminiDelegatingTurnKey{sessionID: sessionID, turnID: turnID}, nil
}
