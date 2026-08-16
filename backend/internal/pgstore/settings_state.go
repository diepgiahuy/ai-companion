package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"companion-server/internal/controlplane"

	"github.com/jackc/pgx/v5"
)

func (s *Store) RecordConfigReport(ctx context.Context, userID, deviceID string, result controlplane.ConfigReportResult) error {
	userID = owner(userID)
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return fmt.Errorf("device_id required")
	}
	if result.Version < 0 {
		return fmt.Errorf("config report version must be non-negative")
	}
	if err := controlplane.Validate(result.Config); err != nil {
		return err
	}
	result.FailureCode = strings.TrimSpace(result.FailureCode)
	if len(result.FailureCode) > 128 {
		return fmt.Errorf("config report failure code too long")
	}
	if result.Applied && result.FailureCode != "" {
		return fmt.Errorf("applied config must not include a failure code")
	}
	if result.ReportedAt.IsZero() {
		result.ReportedAt = time.Now().UTC()
	} else {
		result.ReportedAt = result.ReportedAt.UTC()
	}
	if err := s.ensureTwin(ctx, userID, deviceID); err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var desiredVersion, reportedVersion, lastReportVersion int64
	var lastReportState string
	if err := tx.QueryRow(ctx, `
		SELECT desired_version, reported_version, last_report_version, last_report_state
		FROM device_twins
		WHERE device_id=$1 AND user_id=$2
		FOR UPDATE`, deviceID, userID).Scan(
		&desiredVersion, &reportedVersion, &lastReportVersion, &lastReportState,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("device owner mismatch")
		}
		return err
	}
	if result.Version > desiredVersion {
		return fmt.Errorf("reported config version ahead of desired")
	}
	if result.Version < lastReportVersion {
		return nil
	}

	// Never let a contradictory report roll an already-applied device back.
	// A later successful application of a previously rejected revision is
	// allowed, because that is forward convergence after recovery/reconnect.
	if !result.Applied && result.Version <= reportedVersion {
		return nil
	}
	if result.Applied && result.Version < reportedVersion {
		return nil
	}
	if result.Version == lastReportVersion {
		// Exact duplicate acknowledgement of the current desired revision has no
		// side effect. When a newer desired revision exists, however, a real
		// post-desired report of the older applied revision is useful evidence:
		// persist its timestamp so the product can truthfully classify `stale`.
		if lastReportState == string(controlplane.SettingsApplied) && result.Version == desiredVersion {
			return nil
		}
		if lastReportState == string(controlplane.SettingsRejected) && !result.Applied {
			return nil
		}
	}

	if result.Applied {
		raw, err := json.Marshal(result.Config)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			UPDATE device_twins
			SET reported_json=$1::jsonb,
				reported_version=$2,
				last_report_version=$2,
				last_report_state='applied',
				last_report_error='',
				last_reported_at=$3,
				updated_at=$3
			WHERE device_id=$4 AND user_id=$5`,
			string(raw), result.Version, result.ReportedAt, deviceID, userID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("device owner mismatch")
		}
	} else {
		tag, err := tx.Exec(ctx, `
			UPDATE device_twins
			SET last_report_version=$1,
				last_report_state='rejected',
				last_report_error=$2,
				last_reported_at=$3,
				updated_at=$3
			WHERE device_id=$4 AND user_id=$5`,
			result.Version, result.FailureCode, result.ReportedAt, deviceID, userID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("device owner mismatch")
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) SettingsMetadata(ctx context.Context, userID, deviceID string) (controlplane.SettingsMetadata, error) {
	userID = owner(userID)
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return controlplane.SettingsMetadata{}, fmt.Errorf("device_id required")
	}
	if err := s.ensureTwin(ctx, userID, deviceID); err != nil {
		return controlplane.SettingsMetadata{}, err
	}
	var meta controlplane.SettingsMetadata
	var state string
	if err := s.pool.QueryRow(ctx, `
		SELECT desired_version, reported_version, last_report_version,
		       last_report_state, last_report_error, desired_at, last_reported_at
		FROM device_twins
		WHERE device_id=$1 AND user_id=$2`, deviceID, userID).Scan(
		&meta.DesiredVersion,
		&meta.ReportedVersion,
		&meta.LastReportVersion,
		&state,
		&meta.FailureCode,
		&meta.DesiredAt,
		&meta.ReportedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return controlplane.SettingsMetadata{}, fmt.Errorf("device owner mismatch")
		}
		return controlplane.SettingsMetadata{}, err
	}
	meta.LastReportState = controlplane.SettingsState(state)
	meta.DesiredAt = meta.DesiredAt.UTC()
	if meta.ReportedAt != nil {
		reported := meta.ReportedAt.UTC()
		meta.ReportedAt = &reported
	}
	return meta, nil
}

var _ controlplane.SettingsReportRepository = (*Store)(nil)
