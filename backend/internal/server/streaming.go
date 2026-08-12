package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"companion-server/internal/pipeline"
	"companion-server/internal/protocol"
	"companion-server/internal/realtime"

	"github.com/coder/websocket"
)

type streamingTurnMetrics struct {
	AgentTotal     time.Duration
	TTSActive      time.Duration
	FirstSegmentAt time.Duration
}

// processStreamingReply overlaps model generation with sentence/clause TTS.
// The agent goroutine keeps producing deltas while this goroutine synthesizes
// completed clauses sequentially, reducing time-to-first-audio without allowing
// concurrent writes to the device transport.
func (s *session) processStreamingReply(current *turn, agentCtx context.Context, transcript string, agent pipeline.StreamingAgent) (streamingTurnMetrics, error) {
	started := time.Now()
	segments := make(chan string, 8)
	agentDone := make(chan error, 1)

	go func() {
		segmenter := realtime.NewSegmenter()
		emitSegment := func(segment string) error {
			select {
			case <-current.ctx.Done():
				return current.ctx.Err()
			case segments <- segment:
				return nil
			}
		}

		err := agent.Stream(agentCtx, current.id, transcript, func(event pipeline.AgentStreamEvent) error {
			if event.UI != nil {
				if err := s.sendTurnJSON(current.ctx, current, protocol.Message{
					Type: "ui", SessionID: s.id, TurnID: current.id, UI: event.UI,
				}); err != nil {
					return err
				}
			}
			if strings.TrimSpace(event.Status) != "" {
				if err := s.sendTurnJSON(current.ctx, current, protocol.Message{
					Type: "agent_status", SessionID: s.id, TurnID: current.id, State: event.Status,
				}); err != nil {
					return err
				}
			}
			for _, segment := range segmenter.Push(event.TextDelta) {
				if err := emitSegment(segment); err != nil {
					return err
				}
			}
			return nil
		})
		if err == nil {
			for _, segment := range segmenter.Flush() {
				if emitErr := emitSegment(segment); emitErr != nil {
					err = emitErr
					break
				}
			}
		}
		close(segments)
		agentDone <- err
	}()

	var metrics streamingTurnMetrics
	var ttsStartedAt time.Time
	segmentsSeen := 0
	for segment := range segments {
		if !s.isCurrent(current) {
			current.cancel()
			<-agentDone
			return metrics, context.Canceled
		}
		if segmentsSeen == 0 {
			metrics.FirstSegmentAt = time.Since(started)
			ttsStartedAt = time.Now()
			s.setTurnState(current, "speaking")
			if err := s.sendTurnMediaJSON(current.ctx, current, protocol.Message{
				Type: "tts", State: "start", SessionID: s.id, TurnID: current.id,
			}); err != nil {
				current.cancel()
				<-agentDone
				return metrics, err
			}
		}
		segmentsSeen++
		if err := s.sendTurnMediaJSON(current.ctx, current, protocol.Message{
			Type: "tts", State: "sentence_start", SessionID: s.id, TurnID: current.id, Text: segment,
		}); err != nil {
			current.cancel()
			<-agentDone
			return metrics, err
		}
		if err := s.components.TTS.Synthesize(agentCtx, segment, func(frame []byte) error {
			packet, err := s.codec.EncodeDownlink(frame)
			if err != nil {
				return err
			}
			return s.sendTurn(current.ctx, current, outbound{kind: websocket.MessageBinary, data: packet})
		}); err != nil {
			current.cancel()
			<-agentDone
			return metrics, err
		}
		if err := s.sendTurnMediaJSON(current.ctx, current, protocol.Message{
			Type: "tts", State: "sentence_end", SessionID: s.id, TurnID: current.id, Text: segment,
		}); err != nil {
			current.cancel()
			<-agentDone
			return metrics, err
		}
	}

	agentErr := <-agentDone
	metrics.AgentTotal = time.Since(started)
	if !ttsStartedAt.IsZero() {
		metrics.TTSActive = time.Since(ttsStartedAt)
	}
	if agentErr != nil {
		return metrics, agentErr
	}
	if segmentsSeen == 0 {
		return metrics, fmt.Errorf("streaming agent returned no speakable text")
	}
	if !s.isCurrent(current) {
		return metrics, context.Canceled
	}
	if err := s.sendTurnMediaJSON(current.ctx, current, protocol.Message{
		Type: "tts", State: "stop", SessionID: s.id, TurnID: current.id,
	}); err != nil {
		return metrics, err
	}
	return metrics, nil
}
