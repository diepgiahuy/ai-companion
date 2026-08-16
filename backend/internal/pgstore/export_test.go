package pgstore

import "context"

func (s *Store) InsertRelationshipForTest(ctx context.Context, relationshipID, deviceA, deviceB, userA, userB string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO device_relationships(relationship_id, device_a_id, device_b_id, user_a_id, user_b_id, created_at)
		VALUES($1, $2, $3, $4, $5, now())
		ON CONFLICT(device_a_id, device_b_id) WHERE revoked_at IS NULL DO NOTHING`,
		relationshipID, deviceA, deviceB, userA, userB)
	return err
}
