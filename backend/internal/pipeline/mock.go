package pipeline

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"

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
	Frames int
}

func (m MockTTS) Synthesize(ctx context.Context, _ string, emit func([]byte) error) error {
	frames := m.Frames
	if frames == 0 {
		frames = 10
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
	}
	return nil
}
