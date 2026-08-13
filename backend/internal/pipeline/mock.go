package pipeline

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"time"

	"companion-server/internal/protocol"
)

type MockASR struct {
	Transcript string
}

func (m MockASR) Transcribe(_ context.Context, pcm []byte) (string, error) {
	if len(pcm) == 0 {
		return "", fmt.Errorf("empty audio")
	}
	if m.Transcript != "" {
		return m.Transcript, nil
	}
	return fmt.Sprintf("POC received %d audio bytes", len(pcm)), nil
}

type MockAgent struct {
	Reply string
}

func (m MockAgent) Respond(_ context.Context, _, transcript string) (string, error) {
	if m.Reply != "" {
		return m.Reply, nil
	}
	return "Đã nhận: " + transcript, nil
}

type MockTTS struct {
	Frames     int
	FrameDelay time.Duration
}

func (m MockTTS) Synthesize(ctx context.Context, _ string, emit func([]byte) error) error {
	frames := m.Frames
	if frames == 0 {
		frames = 10
	}
	delay := m.FrameDelay
	if delay == 0 {
		// A zero-delay loop can finish the entire synthetic turn before a client
		// has a chance to exercise cancellation/barge-in. A tiny deterministic
		// inter-frame delay better models streaming while keeping tests fast.
		delay = 5 * time.Millisecond
	}
	phase := 0
	for frameIndex := 0; frameIndex < frames; frameIndex++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		frame := make([]byte, protocol.DownlinkSamplesPerFrame*2)
		for sample := 0; sample < protocol.DownlinkSamplesPerFrame; sample++ {
			value := int16(math.Sin(2*math.Pi*440*float64(phase)/protocol.DownlinkSampleRate) * 5000)
			binary.LittleEndian.PutUint16(frame[sample*2:], uint16(value))
			phase++
		}
		if err := emit(frame); err != nil {
			return err
		}
		if frameIndex+1 < frames && delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return nil
}

// MockStreamingAgent exercises the production streaming path without a model
// dependency. Respond preserves compatibility with the stable Agent interface.
type MockStreamingAgent struct {
	Deltas []string
	Delay  time.Duration
}

func (m MockStreamingAgent) Respond(ctx context.Context, turnID, transcript string) (string, error) {
	var b strings.Builder
	err := m.Stream(ctx, turnID, transcript, func(event AgentStreamEvent) error {
		b.WriteString(event.TextDelta)
		return nil
	})
	return b.String(), err
}

func (m MockStreamingAgent) Stream(ctx context.Context, _, _ string, emit func(AgentStreamEvent) error) error {
	deltas := m.Deltas
	if len(deltas) == 0 {
		deltas = []string{"Xin chào bạn,", " mình đang ở chế độ streaming."}
	}
	for _, delta := range deltas {
		if m.Delay > 0 {
			timer := time.NewTimer(m.Delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		if err := emit(AgentStreamEvent{TextDelta: delta}); err != nil {
			return err
		}
	}
	return nil
}
