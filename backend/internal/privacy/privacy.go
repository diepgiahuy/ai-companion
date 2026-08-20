package privacy

import (
	"context"
	"fmt"
	"time"
)

type Policy struct {
	UserID                    string    `json:"user_id"`
	SaveVoiceAudio            bool      `json:"save_voice_audio"`
	VoiceMailPolicy           string    `json:"voice_mail_policy"`
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

type RecordingReferenceRepository interface {
	ReferencedVoiceMemoPaths(context.Context) ([]string, error)
}

type Service struct {
	repo          Repository
	now           func() time.Time
	recordingsDir string
	orphanGrace   time.Duration
}

func New(repo Repository) *Service {
	return &Service{
		repo:          repo,
		now:           time.Now,
		recordingsDir: defaultRecordingsDir(),
		orphanGrace:   time.Hour,
	}
}

func (s *Service) Policy(ctx context.Context, user string) (Policy, error) {
	p, ok, err := s.repo.GetPrivacyPolicy(ctx, user)
	if err != nil {
		return Policy{}, err
	}
	if ok {
		return p, nil
	}
	return Policy{UserID: user, SaveVoiceAudio: false, VoiceMailPolicy: "disabled", LongTermMemoryEnabled: false}, nil
}

// NormalizePolicy applies privacy defaults and validates the persisted policy
// boundary. Callers that write policies directly through a Repository should use
// this helper so browser, tool, and service paths share one validation contract.
func NormalizePolicy(p Policy) (Policy, error) {
	if p.UserID == "" {
		return Policy{}, fmt.Errorf("user_id required")
	}
	if p.VoiceMailPolicy == "" {
		p.VoiceMailPolicy = "disabled"
	}
	if p.VoiceMailPolicy != "disabled" && p.VoiceMailPolicy != "ephemeral" && p.VoiceMailPolicy != "retained" {
		return Policy{}, fmt.Errorf("voice_mail_policy must be disabled, ephemeral, or retained")
	}
	for name, days := range map[string]int{
		"conversation_retention_days": p.ConversationRetentionDays,
		"voice_memo_retention_days":    p.VoiceMemoRetentionDays,
		"memory_retention_days":        p.MemoryRetentionDays,
	} {
		if days < 0 || days > 3650 {
			return Policy{}, fmt.Errorf("%s out of range", name)
		}
	}
	return p, nil
}

func (s *Service) Set(ctx context.Context, p Policy) error {
	normalized, err := NormalizePolicy(p)
	if err != nil {
		return err
	}
	normalized.UpdatedAt = s.now().UTC()
	return s.repo.SetPrivacyPolicy(ctx, normalized)
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
	now := s.now().UTC()
	report, err := s.repo.ApplyRetention(ctx, now)
	if err != nil {
		return RetentionReport{}, err
	}
	references, ok := s.repo.(RecordingReferenceRepository)
	if !ok || s.recordingsDir == "" {
		return report, nil
	}
	paths, err := references.ReferencedVoiceMemoPaths(ctx)
	if err != nil {
		return RetentionReport{}, fmt.Errorf("load voice memo recording references: %w", err)
	}
	orphans, err := findRecordingOrphans(s.recordingsDir, paths, now, s.orphanGrace)
	if err != nil {
		return RetentionReport{}, err
	}
	report.OrphanPaths = appendUniquePaths(report.OrphanPaths, orphans...)
	return report, nil
}
