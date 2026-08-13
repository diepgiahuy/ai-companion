#!/usr/bin/env python3
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
p = ROOT / "backend/internal/server/server.go"
text = p.read_text(encoding="utf-8")

def replace(old, new, count=1):
    global text
    actual = text.count(old)
    if actual != count:
        raise SystemExit(f"server.go drift: expected {count}, found {actual}: {old[:120]!r}")
    text = text.replace(old, new)

replace("\tmaximumMediaQueue   = 24\n\thelloTimeout        = 10 * time.Second\n", "\tmaximumMediaQueue       = 24\n\thelloTimeout            = 10 * time.Second\n\tsessionLoopJoinTimeout  = time.Second\n\tturnCancellationJoinMax = 250 * time.Millisecond\n")
replace("\tctx        context.Context\n\tcancel     context.CancelFunc\n\tgeneration uint64\n", "\tctx        context.Context\n\tcancel     context.CancelFunc\n\tdone       chan struct{}\n\tgeneration uint64\n")
replace('''func (s *session) run(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	defer s.cancelActive()
	defer s.connection.Close(websocket.StatusNormalClosure, "session closed")

	writerDone := make(chan error, 1)
	go func() { writerDone <- s.writeLoop(ctx) }()

	helloCtx, helloCancel := context.WithTimeout(ctx, helloTimeout)
''', '''func (s *session) run(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	defer s.connection.CloseNow()

	loopExited := make(chan struct{}, 2)
	writerDone := make(chan error, 1)
	loopCount := 1
	go func() {
		defer func() { loopExited <- struct{}{} }()
		writerDone <- s.writeLoop(ctx)
	}()

	helloCtx, helloCancel := context.WithTimeout(ctx, helloTimeout)
''')
replace('''	readDone := make(chan error, 1)
	go func() { readDone <- s.readLoop(ctx) }()
	select {
	case err := <-readDone:
		return err
	case err := <-writerDone:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
''', '''	readDone := make(chan error, 1)
	loopCount++
	go func() {
		defer func() { loopExited <- struct{}{} }()
		readDone <- s.readLoop(ctx)
	}()
	var runErr error
	select {
	case runErr = <-readDone:
	case runErr = <-writerDone:
	case <-ctx.Done():
		runErr = ctx.Err()
	}
	cancel()
	s.connection.CloseNow()
	if !s.cancelActiveAndJoin(turnCancellationJoinMax) {
		s.logger.Warn("active turn did not join before session cancellation bound", "session_id", s.id, "join_ms", turnCancellationJoinMax.Milliseconds())
	}
	joinTimer := time.NewTimer(sessionLoopJoinTimeout)
	defer joinTimer.Stop()
	for joined := 0; joined < loopCount; joined++ {
		select {
		case <-loopExited:
		case <-joinTimer.C:
			s.logger.Warn("session loop did not join before shutdown bound", "session_id", s.id, "joined", joined, "expected", loopCount)
			return runErr
		}
	}
	return runErr
}
''')
replace('''	s.active.state = "processing"
	current := s.active
	pcm := append([]byte(nil), current.pcm...)
	s.mu.Unlock()
	go s.processTurn(current, pcm)
''', '''	s.active.state = "processing"
	current := s.active
	current.done = make(chan struct{})
	pcm := append([]byte(nil), current.pcm...)
	s.mu.Unlock()
	go func() {
		defer close(current.done)
		s.processTurn(current, pcm)
	}()
''')
replace('''func (s *session) cancelActive() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != nil {
		s.active.cancel()
		s.active = nil
		s.generation++ // invalidate queued turn-scoped output
	}
}
''', '''func waitForTurn(current *turn, timeout time.Duration) bool {
	if current == nil || current.done == nil {
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-current.done:
		return true
	case <-timer.C:
		return false
	}
}

func (s *session) cancelActiveAndJoin(timeout time.Duration) bool {
	s.mu.Lock()
	current := s.active
	if current != nil {
		current.cancel()
		s.active = nil
		s.generation++
	}
	s.mu.Unlock()
	return waitForTurn(current, timeout)
}
''')
replace('''func (s *session) interruptActive(ctx context.Context, reason string) {
	s.mu.Lock()
	current := s.active
	if current != nil {
		current.cancel()
		s.active = nil
		s.generation++ // invalidate any already-queued old audio/control
	}
	s.mu.Unlock()
	if current != nil {
		observability.RecordTo(s.observer, observability.Event{
			Name: observability.EventTurnInterrupted, Outcome: "cancelled", Reason: reason, Correlation: s.turnCorrelation(current),
		})
''', '''func (s *session) interruptActive(ctx context.Context, reason string) {
	s.mu.Lock()
	current := s.active
	if current != nil {
		current.cancel()
		s.active = nil
		s.generation++
	}
	s.mu.Unlock()
	if current != nil {
		joinStarted := time.Now()
		joined := waitForTurn(current, turnCancellationJoinMax)
		joinDuration := time.Since(joinStarted)
		observability.RecordTo(s.observer, observability.Event{
			Name: observability.EventTurnInterrupted, Outcome: "cancelled", Reason: reason, DurationMS: joinDuration.Milliseconds(), Correlation: s.turnCorrelation(current),
		})
		if !joined {
			s.logger.Warn("interrupted turn exceeded cancellation join bound", "session_id", s.id, "turn_id", current.id, "reason", reason, "join_ms", joinDuration.Milliseconds())
		}
''')
replace('''	if interrupted != nil {
		observability.RecordTo(s.observer, observability.Event{
			Name: observability.EventTurnInterrupted, Outcome: "cancelled", Reason: "barge_in",
			Correlation: observability.Correlation{SessionID: s.id, TurnID: interrupted.id, GenerationID: interrupted.generation},
		})
''', '''	if interrupted != nil {
		joinStarted := time.Now()
		joined := waitForTurn(interrupted, turnCancellationJoinMax)
		joinDuration := time.Since(joinStarted)
		observability.RecordTo(s.observer, observability.Event{
			Name: observability.EventTurnInterrupted, Outcome: "cancelled", Reason: "barge_in", DurationMS: joinDuration.Milliseconds(),
			Correlation: observability.Correlation{SessionID: s.id, TurnID: interrupted.id, GenerationID: interrupted.generation},
		})
		if !joined {
			s.logger.Warn("barge-in turn exceeded cancellation join bound", "session_id", s.id, "turn_id", interrupted.id, "join_ms", joinDuration.Milliseconds())
		}
''')
p.write_text(text, encoding="utf-8")

(ROOT / "backend/internal/server/session_lifecycle_test.go").write_text('''package server\n\nimport (\n\t"testing"\n\t"time"\n)\n\nfunc TestWaitForTurnIsImmediateForListeningAndBoundedForHungProcessing(t *testing.T) {\n\tif !waitForTurn(&turn{}, 5*time.Millisecond) { t.Fatal("listening turn should join immediately") }\n\thung := &turn{done: make(chan struct{})}\n\tstarted := time.Now()\n\tif waitForTurn(hung, 20*time.Millisecond) { t.Fatal("hung turn unexpectedly joined") }\n\tif elapsed := time.Since(started); elapsed < 15*time.Millisecond || elapsed > 250*time.Millisecond { t.Fatalf("bounded join elapsed=%v", elapsed) }\n\tdone := &turn{done: make(chan struct{})}\n\tclose(done.done)\n\tif !waitForTurn(done, time.Second) { t.Fatal("completed turn should join immediately") }\n}\n''', encoding="utf-8")
print("issue28 session lifecycle patch applied")
