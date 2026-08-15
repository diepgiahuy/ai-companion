CREATE TABLE device_claim_deliveries (
    delivery_id text PRIMARY KEY,
    device_id text NOT NULL REFERENCES device_credentials(device_id) ON DELETE CASCADE,
    user_id text NOT NULL,
    credential_ciphertext bytea,
    credential_nonce bytea,
    expires_at timestamptz NOT NULL,
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (length(delivery_id) BETWEEN 16 AND 128),
    CHECK (length(device_id) BETWEEN 1 AND 128),
    CHECK (length(user_id) BETWEEN 1 AND 256),
    CHECK ((credential_ciphertext IS NOT NULL AND credential_nonce IS NOT NULL)
        OR (credential_ciphertext IS NULL AND credential_nonce IS NULL))
);

CREATE INDEX idx_device_claim_deliveries_expiry
    ON device_claim_deliveries (expires_at)
    WHERE credential_ciphertext IS NOT NULL;
