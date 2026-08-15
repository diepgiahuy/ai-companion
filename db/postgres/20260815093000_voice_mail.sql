ALTER TABLE privacy_policies
  ADD COLUMN voice_mail_policy text NOT NULL DEFAULT 'disabled'
    CHECK (voice_mail_policy IN ('disabled','ephemeral','retained'));

CREATE TABLE voice_mail_items (
  id text PRIMARY KEY,
  sender_user_id text NOT NULL,
  sender_device_id text NOT NULL,
  recipient_user_id text NOT NULL,
  recipient_device_id text NOT NULL DEFAULT '',
  object_key text NOT NULL UNIQUE,
  media_format text NOT NULL CHECK (media_format = 'ogg_opus'),
  duration_ms bigint NOT NULL CHECK (duration_ms BETWEEN 1 AND 600000),
  size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 1 AND 33554432),
  checksum_sha256 text NOT NULL CHECK (checksum_sha256 ~ '^[0-9a-f]{64}$'),
  policy text NOT NULL CHECK (policy IN ('ephemeral','retained')),
  state text NOT NULL CHECK (state IN ('pending_upload','unread','claimed','consumed','delete_pending','deleted','expired','rejected')),
  playback_id text NOT NULL DEFAULT '',
  lease_expires_at timestamptz,
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  CHECK ((state = 'claimed') = (playback_id <> '' AND lease_expires_at IS NOT NULL))
);

CREATE INDEX idx_voice_mail_unread
  ON voice_mail_items(recipient_user_id, recipient_device_id, state, created_at, id);
CREATE INDEX idx_voice_mail_cleanup
  ON voice_mail_items(state, expires_at, updated_at);
