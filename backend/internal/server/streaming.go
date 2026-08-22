package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"companion-server/internal/pipeline"
	"companion-server/internal/presentation"
	"companion-server/internal/protocol"
	"companion-server/internal/realtime"

	"github.com/coder/websocket"
)

const agentCancellationJoinTimeout = 2 * time.Second

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
	streamCtx, cancelStream := context.WithCancel(agentCtx)
	defer cancelStream()

	if err := s.sendTurnUIState(current.ctx, current, protocol.UIEmotionThinking, ""); err != nil {
		return streamingTurnMetrics{}, err
	}

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

		err := agent.Stream(streamCtx, current.id, transcript, func(event pipeline.AgentStreamEvent) error {
			if event.UI != nil {
				card, cardErr := presentation.NewCardV1(
					event.UI.Kind,
					event.UI.Title,
					event.UI.Primary,
					event.UI.Secondary,
					event.UI.Progress,
				)
				if cardErr != nil {
					s.logger.Warn("drop invalid semantic ui card", "error", cardErr)
				} else if err := s.sendTurnJSON(current.ctx, current, protocol.UICardType, protocol.UICardPayload{UI: card}); err != nil {
					return err
				}
			}
			if strings.TrimSpace(event.Status) != "" {
				if err := s.sendTurnJSON(current.ctx, current, protocol.AgentStatusType, protocol.AgentStatusPayload{State: event.Status}); err != nil {
					return err
				}
				if event.Status == "tool_running" && strings.TrimSpace(event.ToolName) != "" {
					if err := s.sendTurnUIState(current.ctx, current, protocol.UIEmotionToolExecuting, event.ToolName); err != nil {
						return err
					}
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
	for {
		select {
		case <-current.ctx.Done():
			cancelStream()
			if err := waitAgentDone(agentDone); err != nil {
				return metrics, err
			}
			return metrics, context.Canceled
		case segment, ok := <-segments:
			if !ok {
				goto streamComplete
			}
			if !s.isCurrent(current) {
				cancelStream()
				if err := waitAgentDone(agentDone); err != nil {
					return metrics, err
				}
				return metrics, context.Canceled
			}
			if segmentsSeen == 0 {
				metrics.FirstSegmentAt = time.Since(started)
				ttsStartedAt = time.Now()
				s.setTurnState(current, "speaking")
				if err := s.sendTurnUIState(current.ctx, current, protocol.UIEmotionSpeaking, ""); err != nil {
					cancelStream()
					_ = waitAgentDone(agentDone)
					return metrics, err
				}
				if err := s.sendTurnMediaJSON(current.ctx, current, protocol.TTSLifecycleType, protocol.TTSLifecyclePayload{State: "start"}); err != nil {
					cancelStream()
					_ = waitAgentDone(agentDone)
					return metrics, err
				}
			}
			segmentsSeen++
			if err := s.sendTurnMediaJSON(current.ctx, current, protocol.TTSLifecycleType, protocol.TTSLifecyclePayload{State: "sentence_start", Text: segment}); err != nil {
				cancelStream()
				_ = waitAgentDone(agentDone)
				return metrics, err
			}
			if err := s.components.TTS.Synthesize(streamCtx, segment, func(frame []byte) error {
				packet, err := s.codec.EncodeDownlink(frame)
				if err != nil {
					return err
				}
				return s.sendTurnMedia(current.ctx, current, outbound{kind: websocket.MessageBinary, data: packet})
			}); err != nil {
				cancelStream()
				_ = waitAgentDone(agentDone)
				return metrics, err
			}
			if err := s.sendTurnMediaJSON(current.ctx, current, protocol.TTSLifecycleType, protocol.TTSLifecyclePayload{State: "sentence_end", Text: segment}); err != nil {
				cancelStream()
				_ = waitAgentDone(agentDone)
				return metrics, err
			}
		}
	}

streamComplete:
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
	if err := s.sendTurnMediaJSON(current.ctx, current, protocol.TTSLifecycleType, protocol.TTSLifecyclePayload{State: "stop"}); err != nil {
		return metrics, err
	}
	return metrics, nil
}

// sendTurnMedia applies bounded backpressure to turn-scoped media producers.
// This keeps bursty TTS output ordered without converting transient queue pressure
// into a failed turn. Cancellation still stops stale generations promptly.
func (s *session) sendTurnMedia(ctx context.Context, current *turn, message outbound) error {
	if current == nil {
		return fmt.Errorf("turn is required")
	}
	message.turnScoped = true
	message.generation = current.generation
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := waitCtx.Err(); err != nil {
		return err
	}
	select {
	case <-waitCtx.Done():
		return waitCtx.Err()
	case s.mediaWrites <- message:
		return nil
	}
}

func waitAgentDone(done <-chan error) error {
	timer := time.NewTimer(agentCancellationJoinTimeout)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		return fmt.Errorf("streaming agent did not stop within %s after cancellation", agentCancellationJoinTimeout)
	}
}
