package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"companion-server/internal/jobs"
	"companion-server/internal/pipeline"
)

type jobControlStub struct {
	result  jobs.EnqueueResult
	metrics jobs.MetricsSnapshot
	calls   int
}

func (s *jobControlStub) EnqueueRetention(context.Context) (jobs.EnqueueResult, error) {
	s.calls++
	return s.result, nil
}

func (s *jobControlStub) MetricsSnapshot() jobs.MetricsSnapshot { return s.metrics }

func TestJobAdminEndpointsRequireTokenAndExposeBoundedState(t *testing.T) {
	control := &jobControlStub{
		result:  jobs.EnqueueResult{JobID: 42},
		metrics: jobs.MetricsSnapshot{Completed: 3, RetryAttempts: 1},
	}
	handler := New(pipeline.Components{}, nil, WithAdminToken("secret"), WithJobControl(control)).Handler()

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/v1/admin/jobs/retention", nil))
	if unauthorized.Code != http.StatusUnauthorized || control.calls != 0 {
		t.Fatalf("unauthorized status=%d calls=%d", unauthorized.Code, control.calls)
	}

	enqueueRequest := httptest.NewRequest(http.MethodPost, "/v1/admin/jobs/retention", nil)
	enqueueRequest.Header.Set("Authorization", "Bearer secret")
	enqueued := httptest.NewRecorder()
	handler.ServeHTTP(enqueued, enqueueRequest)
	var result jobs.EnqueueResult
	if err := json.Unmarshal(enqueued.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if enqueued.Code != http.StatusAccepted || result.JobID != 42 || control.calls != 1 {
		t.Fatalf("status=%d result=%+v calls=%d", enqueued.Code, result, control.calls)
	}

	metricsRequest := httptest.NewRequest(http.MethodGet, "/v1/admin/jobs/metrics", nil)
	metricsRequest.Header.Set("Authorization", "Bearer secret")
	metricsResponse := httptest.NewRecorder()
	handler.ServeHTTP(metricsResponse, metricsRequest)
	var snapshot jobs.MetricsSnapshot
	if err := json.Unmarshal(metricsResponse.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if metricsResponse.Code != http.StatusOK || snapshot.Completed != 3 || snapshot.RetryAttempts != 1 {
		t.Fatalf("status=%d metrics=%+v", metricsResponse.Code, snapshot)
	}
}
