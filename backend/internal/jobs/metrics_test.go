package jobs

import (
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

func TestMetricsClassifiesRetryTerminalFailureAndLatency(t *testing.T) {
	metrics := &Metrics{}
	metrics.recordEnqueue(false)
	metrics.recordEnqueue(true)
	metrics.Record(&river.Event{Kind: river.EventKindJobFailed, Job: &rivertype.JobRow{Kind: RetentionKind, State: rivertype.JobStateAvailable}, JobStats: &river.JobStatistics{QueueWaitDuration: 12 * time.Millisecond, RunDuration: 8 * time.Millisecond}})
	metrics.Record(&river.Event{Kind: river.EventKindJobCompleted, Job: &rivertype.JobRow{Kind: RetentionKind}, JobStats: &river.JobStatistics{QueueWaitDuration: 4 * time.Millisecond, RunDuration: 20 * time.Millisecond}})
	metrics.Record(&river.Event{Kind: river.EventKindJobFailed, Job: &rivertype.JobRow{Kind: RetentionKind, State: rivertype.JobStateDiscarded}})
	metrics.Record(&river.Event{Kind: river.EventKindJobCancelled, Job: &rivertype.JobRow{Kind: RetentionKind}})

	snapshot := metrics.Snapshot()
	if snapshot.Enqueued != 1 || snapshot.UniqueSkipped != 1 || snapshot.RetryAttempts != 1 || snapshot.Completed != 1 || snapshot.Failed != 1 || snapshot.Cancelled != 1 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	if snapshot.LastQueueWaitMS != 4 || snapshot.MaxQueueWaitMS != 12 || snapshot.LastRunMS != 20 || snapshot.MaxRunMS != 20 || snapshot.LastEventAt == "" {
		t.Fatalf("latency snapshot=%+v", snapshot)
	}
}
