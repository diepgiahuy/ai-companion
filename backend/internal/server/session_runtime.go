package server

import "time"

func waitForWorker(done <-chan error, timeout time.Duration) bool {
	if done == nil {
		return true
	}
	if timeout <= 0 {
		timeout = time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func waitForTurn(current *turn, timeout time.Duration) bool {
	if current == nil || current.done == nil {
		return true
	}
	if timeout <= 0 {
		timeout = time.Second
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
