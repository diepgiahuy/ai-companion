CREATE TABLE pairing_sessions (
    session_id text PRIMARY KEY,
    initiator_user_id text NOT NULL,
    initiator_device_id text NOT NULL REFERENCES device_credentials(device_id) ON DELETE RESTRICT,
    peer_user_id text NOT NULL,
    peer_device_id text NOT NULL REFERENCES device_credentials(device_id) ON DELETE RESTRICT,
    proximity_evidence_id text NOT NULL,
    initiator_nonce text NOT NULL,
    peer_nonce text NOT NULL,
    initiator_confirmed_at timestamptz,
    peer_confirmed_at timestamptz,
    expires_at timestamptz NOT NULL,
    relationship_id text,
    state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','paired','cancelled','expired')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK(initiator_device_id <> peer_device_id)
);
CREATE INDEX idx_pairing_sessions_participants ON pairing_sessions(initiator_device_id,peer_device_id,state,expires_at);
CREATE INDEX idx_pairing_sessions_peer ON pairing_sessions(peer_device_id,state,expires_at);

CREATE TABLE device_relationships (
    relationship_id text PRIMARY KEY,
    device_a_id text NOT NULL REFERENCES device_credentials(device_id) ON DELETE RESTRICT,
    device_b_id text NOT NULL REFERENCES device_credentials(device_id) ON DELETE RESTRICT,
    user_a_id text NOT NULL,
    user_b_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK(device_a_id < device_b_id),
    UNIQUE(device_a_id,device_b_id)
);
CREATE INDEX idx_device_relationships_a ON device_relationships(device_a_id,created_at DESC);
CREATE INDEX idx_device_relationships_b ON device_relationships(device_b_id,created_at DESC);
