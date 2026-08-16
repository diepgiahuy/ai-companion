-- Add relationship revocation lifecycle and bind new voice mail items to the exact relationship generation.
-- relationship_id is intentionally nullable so already-committed legacy mail remains readable/playable
-- without inventing historical relationship authority.
ALTER TABLE device_relationships DROP CONSTRAINT device_relationships_device_a_id_device_b_id_key;
ALTER TABLE device_relationships ADD COLUMN revoked_at timestamptz;

CREATE UNIQUE INDEX idx_device_relationships_active_pair ON device_relationships(device_a_id, device_b_id) WHERE revoked_at IS NULL;
CREATE INDEX idx_device_relationships_active_a ON device_relationships(device_a_id, created_at DESC) WHERE revoked_at IS NULL;
CREATE INDEX idx_device_relationships_active_b ON device_relationships(device_b_id, created_at DESC) WHERE revoked_at IS NULL;

ALTER TABLE voice_mail_items ADD COLUMN relationship_id text REFERENCES device_relationships(relationship_id);
CREATE INDEX idx_voice_mail_relationship ON voice_mail_items(relationship_id);
