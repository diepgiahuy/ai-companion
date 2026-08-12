package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type RuntimeConfig struct {
	SmartVADEnabled *bool  `json:"smart_vad_enabled,omitempty"`
	VADThreshold    *int   `json:"vad_threshold,omitempty"`
	VADSilenceMS    *int   `json:"vad_silence_ms,omitempty"`
	VADMinSpeechMS  *int   `json:"vad_min_speech_ms,omitempty"`
	IdleAfterMS     *int   `json:"idle_after_ms,omitempty"`
	AlarmVisibleMS  *int   `json:"alarm_visible_ms,omitempty"`
	Locale          string `json:"locale,omitempty"`
	Timezone        string `json:"timezone,omitempty"`
	VoiceKey        string `json:"voice_key,omitempty"`
}
type Twin struct {
	DeviceID        string        `json:"device_id"`
	UserID          string        `json:"user_id"`
	Desired         RuntimeConfig `json:"desired"`
	DesiredVersion  int64         `json:"desired_version"`
	Reported        RuntimeConfig `json:"reported"`
	ReportedVersion int64         `json:"reported_version"`
	UpdatedAt       time.Time     `json:"updated_at"`
}
type Repository interface {
	GetTwin(context.Context, string, string) (Twin, error)
	SetDesired(context.Context, string, string, RuntimeConfig) (Twin, error)
	Report(context.Context, string, string, int64, RuntimeConfig) error
}
type ScopedRepository interface {
	GetConfigOverride(context.Context, string, string) (RuntimeConfig, bool, error)
	SetConfigOverride(context.Context, string, string, RuntimeConfig) error
}
type ResolutionContext struct{ UserID, DeviceID, TenantID, Plan string }
type Service struct {
	repo     Repository
	defaults RuntimeConfig
}

func New(repo Repository, defaults RuntimeConfig) *Service {
	return &Service{repo: repo, defaults: defaults}
}
func (s *Service) Manifest(ctx context.Context, user, device string) (Twin, error) {
	return s.ManifestFor(ctx, ResolutionContext{UserID: user, DeviceID: device})
}
func (s *Service) ManifestFor(ctx context.Context, rc ResolutionContext) (Twin, error) {
	t, e := s.repo.GetTwin(ctx, rc.UserID, rc.DeviceID)
	if e != nil {
		return Twin{}, e
	}
	resolved := s.defaults
	if sr, ok := s.repo.(ScopedRepository); ok {
		for _, scope := range [][2]string{{"global", "*"}, {"tenant", rc.TenantID}, {"plan", rc.Plan}, {"user", rc.UserID}} {
			if scope[1] == "" {
				continue
			}
			if c, found, e := sr.GetConfigOverride(ctx, scope[0], scope[1]); e != nil {
				return Twin{}, e
			} else if found {
				resolved = merge(resolved, c)
			}
		}
	}
	t.Desired = merge(resolved, t.Desired)
	return t, nil
}
func (s *Service) SetDesired(ctx context.Context, user, device string, patch RuntimeConfig) (Twin, error) {
	current, e := s.repo.GetTwin(ctx, user, device)
	if e != nil {
		return Twin{}, e
	}
	merged := merge(current.Desired, patch)
	if e := Validate(merged); e != nil {
		return Twin{}, e
	}
	_, e = s.repo.SetDesired(ctx, user, device, merged)
	if e != nil {
		return Twin{}, e
	}
	return s.Manifest(ctx, user, device)
}
func (s *Service) SetScopedConfig(ctx context.Context, scopeType, scopeID string, patch RuntimeConfig) error {
	if scopeType != "global" && scopeType != "tenant" && scopeType != "plan" && scopeType != "user" {
		return fmt.Errorf("unsupported config scope")
	}
	if scopeType == "global" {
		scopeID = "*"
	}
	if scopeID == "" {
		return fmt.Errorf("scope id required")
	}
	sr, ok := s.repo.(ScopedRepository)
	if !ok {
		return fmt.Errorf("scoped config unsupported")
	}
	current, _, e := sr.GetConfigOverride(ctx, scopeType, scopeID)
	if e != nil {
		return e
	}
	merged := merge(current, patch)
	if e := Validate(merged); e != nil {
		return e
	}
	return sr.SetConfigOverride(ctx, scopeType, scopeID, merged)
}
func (s *Service) GetScopedConfig(ctx context.Context, scopeType, scopeID string) (RuntimeConfig, bool, error) {
	sr, ok := s.repo.(ScopedRepository)
	if !ok {
		return RuntimeConfig{}, false, fmt.Errorf("scoped config unsupported")
	}
	if scopeType == "global" {
		scopeID = "*"
	}
	return sr.GetConfigOverride(ctx, scopeType, scopeID)
}
func (s *Service) Report(ctx context.Context, user, device string, v int64, c RuntimeConfig) error {
	if e := Validate(c); e != nil {
		return e
	}
	return s.repo.Report(ctx, user, device, v, c)
}
func Validate(c RuntimeConfig) error {
	chk := func(name string, v *int, min, max int) error {
		if v != nil && (*v < min || *v > max) {
			return fmt.Errorf("%s out of range", name)
		}
		return nil
	}
	if e := chk("vad_threshold", c.VADThreshold, 1, 100000); e != nil {
		return e
	}
	if e := chk("vad_silence_ms", c.VADSilenceMS, 100, 5000); e != nil {
		return e
	}
	if e := chk("vad_min_speech_ms", c.VADMinSpeechMS, 50, 5000); e != nil {
		return e
	}
	if e := chk("idle_after_ms", c.IdleAfterMS, 1000, 3600000); e != nil {
		return e
	}
	if e := chk("alarm_visible_ms", c.AlarmVisibleMS, 1000, 3600000); e != nil {
		return e
	}
	if c.Locale != "" {
		parts := strings.Split(c.Locale, "-")
		if len(parts[0]) < 2 || len(parts[0]) > 3 {
			return fmt.Errorf("locale must be a BCP-47 style tag")
		}
	}
	if c.Timezone != "" {
		if _, err := time.LoadLocation(c.Timezone); err != nil {
			return fmt.Errorf("invalid timezone")
		}
	}
	if len(c.VoiceKey) > 128 {
		return fmt.Errorf("voice_key too long")
	}
	return nil
}
func merge(a, b RuntimeConfig) RuntimeConfig {
	if b.SmartVADEnabled != nil {
		a.SmartVADEnabled = b.SmartVADEnabled
	}
	if b.VADThreshold != nil {
		a.VADThreshold = b.VADThreshold
	}
	if b.VADSilenceMS != nil {
		a.VADSilenceMS = b.VADSilenceMS
	}
	if b.VADMinSpeechMS != nil {
		a.VADMinSpeechMS = b.VADMinSpeechMS
	}
	if b.IdleAfterMS != nil {
		a.IdleAfterMS = b.IdleAfterMS
	}
	if b.AlarmVisibleMS != nil {
		a.AlarmVisibleMS = b.AlarmVisibleMS
	}
	if b.Locale != "" {
		a.Locale = b.Locale
	}
	if b.Timezone != "" {
		a.Timezone = b.Timezone
	}
	if b.VoiceKey != "" {
		a.VoiceKey = b.VoiceKey
	}
	return a
}

type ConfigField struct {
	Key             string   `json:"key"`
	Dynamic         bool     `json:"dynamic"`
	RestartRequired bool     `json:"restart_required"`
	Sensitive       bool     `json:"sensitive"`
	Min             *int     `json:"min,omitempty"`
	Max             *int     `json:"max,omitempty"`
	Scopes          []string `json:"scopes"`
}

func ConfigSchema() []ConfigField {
	ptr := func(v int) *int { return &v }
	scopes := []string{"global", "tenant", "plan", "user", "device"}
	return []ConfigField{
		{Key: "smart_vad_enabled", Dynamic: true, Scopes: scopes},
		{Key: "vad_threshold", Dynamic: true, Min: ptr(1), Max: ptr(65535), Scopes: scopes},
		{Key: "vad_silence_ms", Dynamic: true, Min: ptr(100), Max: ptr(5000), Scopes: scopes},
		{Key: "vad_min_speech_ms", Dynamic: true, Min: ptr(50), Max: ptr(5000), Scopes: scopes},
		{Key: "idle_after_ms", Dynamic: true, Min: ptr(1000), Max: ptr(3600000), Scopes: scopes},
		{Key: "alarm_visible_ms", Dynamic: true, Min: ptr(1000), Max: ptr(3600000), Scopes: scopes},
		{Key: "locale", Dynamic: true, Scopes: scopes}, {Key: "timezone", Dynamic: true, Scopes: scopes}, {Key: "voice_key", Dynamic: true, Scopes: scopes},
	}
}

func Encode(c RuntimeConfig) string { b, _ := json.Marshal(c); return string(b) }
func Decode(s string) (RuntimeConfig, error) {
	var c RuntimeConfig
	e := json.Unmarshal([]byte(s), &c)
	return c, e
}

type EvalContext struct{ UserID, DeviceID, TenantID, Plan, Locale, Country, Firmware, Hardware string }
type Flag struct {
	Key          string            `json:"key"`
	Enabled      bool              `json:"enabled"`
	Rollout      int               `json:"rollout"`
	RequiredPlan string            `json:"required_plan,omitempty"`
	Variants     map[string]string `json:"variants,omitempty"`
	Lifecycle    string            `json:"lifecycle,omitempty"`
	Owner        string            `json:"owner,omitempty"`
	ExpiresAt    *time.Time        `json:"expires_at,omitempty"`
}
type FlagRepository interface {
	Flags(context.Context) ([]Flag, error)
}
type FlagAdminRepository interface {
	FlagRepository
	SetFlag(context.Context, Flag) error
}

func (s *Service) Flags(ctx context.Context) ([]Flag, error) {
	r, ok := s.repo.(FlagRepository)
	if !ok {
		return nil, fmt.Errorf("feature flags unsupported")
	}
	return r.Flags(ctx)
}
func (s *Service) SetFlag(ctx context.Context, f Flag) error {
	r, ok := s.repo.(FlagAdminRepository)
	if !ok {
		return fmt.Errorf("feature flag admin unsupported")
	}
	if f.Key == "" || f.Rollout < 0 || f.Rollout > 100 {
		return fmt.Errorf("invalid flag")
	}
	if f.Lifecycle == "" {
		f.Lifecycle = "released"
	}
	switch f.Lifecycle {
	case "draft", "internal", "beta", "released", "deprecated", "removed":
	default:
		return fmt.Errorf("invalid flag lifecycle")
	}
	return r.SetFlag(ctx, f)
}

type FeatureProvider struct{ repo FlagRepository }

func NewFeatures(r FlagRepository) *FeatureProvider { return &FeatureProvider{repo: r} }
func (f *FeatureProvider) Enabled(ctx context.Context, key string, e EvalContext, fallback bool) bool {
	xs, err := f.repo.Flags(ctx)
	if err != nil {
		return fallback
	}
	for _, x := range xs {
		if x.Key != key {
			continue
		}
		if x.Lifecycle == "removed" || x.Lifecycle == "deprecated" && !x.Enabled {
			return false
		}
		if x.ExpiresAt != nil && !x.ExpiresAt.After(time.Now()) {
			return false
		}
		if !x.Enabled {
			return false
		}
		if x.RequiredPlan != "" && x.RequiredPlan != e.Plan {
			return false
		}
		if x.Rollout <= 0 {
			return false
		}
		if x.Rollout >= 100 {
			return true
		}
		return stableBucket(key+":"+e.UserID+":"+e.DeviceID) < x.Rollout
	}
	return fallback
}
func stableBucket(s string) int {
	n := 0
	for _, r := range s {
		n = (n*131 + int(r)) % 10000
	}
	return n % 100
}
func SortedFlagKeys(xs []Flag) []string {
	o := make([]string, 0, len(xs))
	for _, x := range xs {
		o = append(o, x.Key)
	}
	sort.Strings(o)
	return o
}
