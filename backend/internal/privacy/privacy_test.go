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

func TestPrivacyDefaultsToDenyUntilExplicitConsent(t *testing.T) {
	r := &repoStub{}
	s := New(r)
	p, err := s.Policy(context.Background(), "u")
	if err != nil {
		t.Fatal(err)
	}
	if p.UserID != "u" || p.SaveVoiceAudio || p.LongTermMemoryEnabled {
		t.Fatalf("missing policy must deny persistence: %+v", p)
	}
	if s.MemoryAllowed(context.Background(), "u") || s.VoiceAudioAllowed(context.Background(), "u") {
		t.Fatal("missing policy must deny memory and voice persistence")
	}

	if err := s.Set(context.Background(), Policy{UserID: "u", SaveVoiceAudio: true, LongTermMemoryEnabled: true}); err != nil {
		t.Fatal(err)
	}
	if !s.MemoryAllowed(context.Background(), "u") || !s.VoiceAudioAllowed(context.Background(), "u") {
		t.Fatal("explicit consent should enable opted-in persistence")
	}

	if err := s.Set(context.Background(), Policy{UserID: "u", SaveVoiceAudio: false, LongTermMemoryEnabled: false}); err != nil {
		t.Fatal(err)
	}
	if s.MemoryAllowed(context.Background(), "u") || s.VoiceAudioAllowed(context.Background(), "u") {
		t.Fatal("revocation must disable memory and voice persistence")
	}
}
