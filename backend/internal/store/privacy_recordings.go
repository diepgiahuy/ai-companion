package store

import "context"

func (s *Store) ReferencedVoiceMemoPaths(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path FROM voice_memos WHERE path<>'' ORDER BY id`)
	if err != nil { return nil, err }
	defer rows.Close()
	paths := make([]string, 0)
	for rows.Next() { var path string; if err := rows.Scan(&path); err != nil { return nil, err }; paths = append(paths, path) }
	return paths, rows.Err()
}
