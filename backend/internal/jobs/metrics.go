package jobs

import (
	"sync/atomic"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

type MetricsSnapshot struct {
	Enqueued        uint64 `json:"enqueued"`
	UniqueSkipped   uint64 `json:"unique_skipped"`
	Completed       uint64 `json:"completed"`
	RetryAttempts   uint64 `json:"retry_attempts"`
	Failed          uint64 `json:"failed"`
	Cancelled       uint64 `json:"cancelled"`
	LastQueueWaitMS int64  `json:"last_queue_wait_ms"`
	MaxQueueWaitMS  int64  `json:"max_queue_wait_ms"`
	LastRunMS       int64  `json:"last_run_ms"`
	MaxRunMS        int64  `json:"max_run_ms"`
	LastEventAt     string `json:"last_event_at,omitempty"`
}

type Metrics struct {
	enqueued, uniqueSkipped, completed atomic.Uint64
	retryAttempts, failed, cancelled   atomic.Uint64
	lastQueueWaitMS, maxQueueWaitMS    atomic.Int64
	lastRunMS, maxRunMS                atomic.Int64
	lastEventUnixNano                  atomic.Int64
}

func (m *Metrics) recordEnqueue(uniqueSkipped bool) {
	if uniqueSkipped {
		m.uniqueSkipped.Add(1)
		return
	}
	m.enqueued.Add(1)
}

func (m *Metrics) Record(event *river.Event) {
	if event == nil || event.Job == nil || event.Job.Kind != RetentionKind {
		return
	}
	switch event.Kind {
	case river.EventKindJobCompleted:
		m.completed.Add(1)
	case river.EventKindJobFailed:
		if event.Job.State == rivertype.JobStateDiscarded {
			m.failed.Add(1)
		} else {
			m.retryAttempts.Add(1)
		}
	case river.EventKindJobCancelled:
		m.cancelled.Add(1)
	}
	if event.JobStats != nil {
		queueWait := event.JobStats.QueueWaitDuration.Milliseconds()
		run := event.JobStats.RunDuration.Milliseconds()
		m.lastQueueWaitMS.Store(queueWait)
		m.lastRunMS.Store(run)
		storeMax(&m.maxQueueWaitMS, queueWait)
		storeMax(&m.maxRunMS, run)
	}
	m.lastEventUnixNano.Store(time.Now().UTC().UnixNano())
}

func (m *Metrics) Snapshot() MetricsSnapshot {
	snapshot := MetricsSnapshot{
		Enqueued: m.enqueued.Load(), UniqueSkipped: m.uniqueSkipped.Load(),
		Completed: m.completed.Load(), RetryAttempts: m.retryAttempts.Load(),
		Failed: m.failed.Load(), Cancelled: m.cancelled.Load(),
		LastQueueWaitMS: m.lastQueueWaitMS.Load(), MaxQueueWaitMS: m.maxQueueWaitMS.Load(),
		LastRunMS: m.lastRunMS.Load(), MaxRunMS: m.maxRunMS.Load(),
	}
	if value := m.lastEventUnixNano.Load(); value > 0 {
		snapshot.LastEventAt = time.Unix(0, value).UTC().Format(time.RFC3339Nano)
	}
	return snapshot
}

func storeMax(target *atomic.Int64, value int64) {
	for current := target.Load(); value > current; current = target.Load() {
		if target.CompareAndSwap(current, value) {
			return
		}
	}
}
