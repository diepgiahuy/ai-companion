CREATE TABLE device_claim_sessions (
    session_id text PRIMARY KEY,
    device_id text NOT NULL,
    bootstrap_id text NOT NULL,
    device_code_hash text NOT NULL UNIQUE,
    user_code_hash text NOT NULL,
    user_code_plain text NOT NULL,
    owner_user_id text,
    status text NOT NULL DEFAULT 'pending',
    claim_authorization text,
    claim_auth_expires_at timestamptz,
    expires_at timestamptz NOT NULL,
    approved_at timestamptz,
    consumed_at timestamptz,
    last_poll_at timestamptz,
    poll_count integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (length(session_id) BETWEEN 16 AND 128),
    CHECK (length(device_id) BETWEEN 1 AND 128),
    CHECK (length(bootstrap_id) BETWEEN 1 AND 128),
    CHECK (length(device_code_hash) = 64),
    CHECK (length(user_code_hash) = 64),
    CHECK (length(user_code_plain) BETWEEN 4 AND 32),
    CHECK (owner_user_id IS NULL OR length(owner_user_id) BETWEEN 1 AND 256),
    CHECK (status IN ('pending', 'approved', 'denied', 'expired', 'consumed')),
    CHECK (poll_count >= 0)
);

CREATE INDEX idx_device_claim_sessions_device_code_hash
    ON device_claim_sessions (device_code_hash);

CREATE INDEX idx_device_claim_sessions_expires_at
    ON device_claim_sessions (expires_at)
    WHERE status = 'pending';

CREATE TABLE claim_rate_limits (
    rate_key text PRIMARY KEY,
    attempt_count integer NOT NULL DEFAULT 1,
    window_started_at timestamptz NOT NULL DEFAULT now(),
    CHECK (length(rate_key) BETWEEN 1 AND 256),
    CHECK (attempt_count >= 0)
);
