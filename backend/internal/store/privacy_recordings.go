package store

import "context"

// ReferencedVoiceMemoPaths returns the recording paths still owned by durable
// voice-memo rows. Privacy retention uses this read-only view to distinguish
// abandoned filesystem blobs from live user data before cleanup.
func (s *Store) ReferencedVoiceMemoPaths(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path FROM voice_memos WHERE path<>'' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	paths := make([]string, 0)
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, rows.Err()
}
