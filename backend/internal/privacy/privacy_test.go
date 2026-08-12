package privacy

import (
	"context"
	"testing"
	"time"
)

type repoStub struct {
	p      Policy
	ok     bool
	report RetentionReport
}

func (r *repoStub) GetPrivacyPolicy(context.Context, string) (Policy, bool, error) {
	return r.p, r.ok, nil
}
func (r *repoStub) SetPrivacyPolicy(_ context.Context, p Policy) error {
	r.p = p
	r.ok = true
	return nil
}
func (r *repoStub) ApplyRetention(context.Context, time.Time) (RetentionReport, error) {
	return r.report, nil
}
func TestPrivacyDefaultsAndExplicitDisable(t *testing.T) {
	r := &repoStub{}
	s := New(r)
	p, e := s.Policy(context.Background(), "u")
	if e != nil || !p.SaveVoiceAudio || !p.LongTermMemoryEnabled {
		t.Fatalf("p=%+v e=%v", p, e)
	}
	if e := s.Set(context.Background(), Policy{UserID: "u", SaveVoiceAudio: false, LongTermMemoryEnabled: false}); e != nil {
		t.Fatal(e)
	}
	if s.MemoryAllowed(context.Background(), "u") {
		t.Fatal("memory should be disabled")
	}
}
