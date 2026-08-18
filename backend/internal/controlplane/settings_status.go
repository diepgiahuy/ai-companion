package controlplane

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type SettingsState string

const (
	SettingsUnknown   SettingsState = "unknown"
	SettingsRequested SettingsState = "requested"
	SettingsApplied   SettingsState = "applied"
	SettingsRejected  SettingsState = "rejected"
	SettingsStale     SettingsState = "stale"
	SettingsOffline   SettingsState = "offline"
)

type ConfigReportResult struct {
	Version     int64
	Applied     bool
	Config      RuntimeConfig
	FailureCode string
	ReportedAt  time.Time
}

type SettingsMetadata struct {
	DesiredVersion    int64
	ReportedVersion   int64
	LastReportVersion int64
	LastReportState   SettingsState
	FailureCode       string
	DesiredAt         time.Time
	ReportedAt        *time.Time
}

type SettingsStatus struct {
	State             SettingsState `json:"state"`
	Online            bool          `json:"online"`
	DesiredVersion    int64         `json:"desired_version"`
	ReportedVersion   int64         `json:"reported_version"`
	LastReportVersion int64         `json:"last_report_version"`
	FailureCode       string        `json:"failure_code,omitempty"`
	DesiredAt         time.Time     `json:"desired_at,omitempty"`
	ReportedAt        *time.Time    `json:"reported_at,omitempty"`
}

type SettingsReportRepository interface {
	RecordConfigReport(context.Context, string, string, ConfigReportResult) error
	SettingsMetadata(context.Context, string, string) (SettingsMetadata, error)
}

func (s *Service) ReportResult(ctx context.Context, user, device string, result ConfigReportResult) error {
	if result.Version < 0 {
		return fmt.Errorf("config report version must be non-negative")
	}
	if err := Validate(result.Config); err != nil {
		return err
	}
	result.FailureCode = strings.TrimSpace(result.FailureCode)
	if len(result.FailureCode) > 128 {
		return fmt.Errorf("config failure code too long")
	}
	if result.Applied && result.FailureCode != "" {
		return fmt.Errorf("applied config must not include a failure code")
	}
	if result.ReportedAt.IsZero() {
		result.ReportedAt = time.Now().UTC()
	} else {
		result.ReportedAt = result.ReportedAt.UTC()
	}
	repo, ok := s.repo.(SettingsReportRepository)
	if !ok {
		if !result.Applied {
			return fmt.Errorf("config rejection reporting unsupported")
		}
		return s.Report(ctx, user, device, result.Version, result.Config)
	}
	return repo.RecordConfigReport(ctx, user, device, result)
}

func (s *Service) SettingsStatus(ctx context.Context, user, device string, online bool) (SettingsStatus, error) {
	repo, ok := s.repo.(SettingsReportRepository)
	if !ok {
		return SettingsStatus{State: SettingsUnknown, Online: online}, nil
	}
	meta, err := repo.SettingsMetadata(ctx, user, device)
	if err != nil {
		return SettingsStatus{}, err
	}
	state := deriveSettingsState(meta, online)
	return SettingsStatus{
		State:             state,
		Online:            online,
		DesiredVersion:    meta.DesiredVersion,
		ReportedVersion:   meta.ReportedVersion,
		LastReportVersion: meta.LastReportVersion,
		FailureCode:       meta.FailureCode,
		DesiredAt:         meta.DesiredAt.UTC(),
		ReportedAt:        utcPtr(meta.ReportedAt),
	}, nil
}

func deriveSettingsState(meta SettingsMetadata, online bool) SettingsState {
	if meta.LastReportState == SettingsRejected && meta.LastReportVersion == meta.DesiredVersion && meta.DesiredVersion > 0 {
		return SettingsRejected
	}
	if meta.DesiredVersion > 0 && meta.ReportedVersion == meta.DesiredVersion && meta.LastReportState == SettingsApplied {
		return SettingsApplied
	}
	if meta.DesiredVersion > meta.ReportedVersion {
		if !online {
			return SettingsOffline
		}
		if meta.ReportedAt != nil && !meta.DesiredAt.IsZero() && !meta.ReportedAt.Before(meta.DesiredAt) && meta.LastReportVersion < meta.DesiredVersion {
			return SettingsStale
		}
		return SettingsRequested
	}
	if meta.ReportedVersion > 0 && meta.LastReportState == SettingsApplied {
		return SettingsApplied
	}
	return SettingsUnknown
}

func utcPtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	t := value.UTC()
	return &t
}
