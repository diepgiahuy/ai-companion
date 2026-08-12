package privacy

import (
	"context"
	"fmt"
	"time"
)

type Policy struct {
	UserID                    string    `json:"user_id"`
	SaveVoiceAudio            bool      `json:"save_voice_audio"`
	LongTermMemoryEnabled     bool      `json:"long_term_memory_enabled"`
	ConversationRetentionDays int       `json:"conversation_retention_days"`
	VoiceMemoRetentionDays    int       `json:"voice_memo_retention_days"`
	MemoryRetentionDays       int       `json:"memory_retention_days"`
	UpdatedAt                 time.Time `json:"updated_at"`
}
type RetentionReport struct {
	ConversationRows int      `json:"conversation_rows"`
	MemoryRows       int      `json:"memory_rows"`
	VoiceMemoRows    int      `json:"voice_memo_rows"`
	OrphanPaths      []string `json:"orphan_paths,omitempty"`
}
type Repository interface {
	GetPrivacyPolicy(context.Context, string) (Policy, bool, error)
	SetPrivacyPolicy(context.Context, Policy) error
	ApplyRetention(context.Context, time.Time) (RetentionReport, error)
}
type Service struct {
	repo Repository
	now  func() time.Time
}

func New(repo Repository) *Service { return &Service{repo: repo, now: time.Now} }
func (s *Service) Policy(ctx context.Context, user string) (Policy, error) {
	p, ok, err := s.repo.GetPrivacyPolicy(ctx, user)
	if err != nil {
		return Policy{}, err
	}
	if ok {
		return p, nil
	}
	return Policy{UserID: user, SaveVoiceAudio: true, LongTermMemoryEnabled: true}, nil
}
func (s *Service) Set(ctx context.Context, p Policy) error {
	if p.UserID == "" {
		return fmt.Errorf("user_id required")
	}
	for name, days := range map[string]int{"conversation_retention_days": p.ConversationRetentionDays, "voice_memo_retention_days": p.VoiceMemoRetentionDays, "memory_retention_days": p.MemoryRetentionDays} {
		if days < 0 || days > 3650 {
			return fmt.Errorf("%s out of range", name)
		}
	}
	p.UpdatedAt = s.now().UTC()
	return s.repo.SetPrivacyPolicy(ctx, p)
}
func (s *Service) MemoryAllowed(ctx context.Context, user string) bool {
	p, e := s.Policy(ctx, user)
	return e == nil && p.LongTermMemoryEnabled
}
func (s *Service) VoiceAudioAllowed(ctx context.Context, user string) bool {
	p, e := s.Policy(ctx, user)
	return e == nil && p.SaveVoiceAudio
}
func (s *Service) ApplyRetention(ctx context.Context) (RetentionReport, error) {
	return s.repo.ApplyRetention(ctx, s.now().UTC())
}
