-- Preserve desired and reported settings as separate facts. Product-facing
-- requested/offline/stale states are derived from these authoritative facts plus
-- the live Protocol-v2 session hub; they are not guessed from a desired write.
ALTER TABLE device_twins
  ADD COLUMN desired_at timestamptz NOT NULL DEFAULT now(),
  ADD COLUMN last_report_version bigint NOT NULL DEFAULT 0 CHECK(last_report_version >= 0),
  ADD COLUMN last_report_state text NOT NULL DEFAULT 'unknown'
    CHECK(last_report_state IN ('unknown','applied','rejected')),
  ADD COLUMN last_report_error text NOT NULL DEFAULT '',
  ADD COLUMN last_reported_at timestamptz;

-- Existing reported_version/report payloads pre-date explicit report-state
-- metadata. Preserve them as applied facts instead of inventing a rejection or
-- resetting current state during migration.
UPDATE device_twins
SET desired_at = updated_at,
    last_report_version = reported_version,
    last_report_state = CASE WHEN reported_version > 0 THEN 'applied' ELSE 'unknown' END,
    last_reported_at = CASE WHEN reported_version > 0 THEN updated_at ELSE NULL END;

-- SetDesired already increments desired_version transactionally. Keep the
-- desired timestamp tied to that exact revision without adding a second write
-- path or relying on updated_at, which also changes on device reports.
CREATE OR REPLACE FUNCTION companion_twin_desired_timestamp() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.desired_version IS DISTINCT FROM OLD.desired_version THEN
    NEW.desired_at = clock_timestamp();
  END IF;
  RETURN NEW;
END
$$;

CREATE TRIGGER trg_twins_desired_timestamp
BEFORE UPDATE ON device_twins
FOR EACH ROW EXECUTE FUNCTION companion_twin_desired_timestamp();
