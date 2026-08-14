package privacy

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func defaultRecordingsDir() string {
	if configured := strings.TrimSpace(os.Getenv("COMPANION_RECORDINGS_DIR")); configured != "" { return configured }
	return filepath.Join("data", "recordings")
}
func canonicalPath(path string) (string, error) { return filepath.Abs(filepath.Clean(path)) }
func findRecordingOrphans(root string, referenced []string, now time.Time, grace time.Duration) ([]string, error) {
	root = strings.TrimSpace(root)
	if root == "" { return nil, nil }
	if grace < 0 { return nil, fmt.Errorf("recording orphan grace must not be negative") }
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) { return nil, nil }
	if err != nil { return nil, fmt.Errorf("read recordings directory: %w", err) }
	references := make(map[string]struct{}, len(referenced))
	for _, path := range referenced {
		if strings.TrimSpace(path) == "" { continue }
		canonical, err := canonicalPath(path)
		if err != nil { return nil, fmt.Errorf("canonicalize referenced recording path: %w", err) }
		references[canonical] = struct{}{}
	}
	orphans := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.EqualFold(filepath.Ext(entry.Name()), ".wav") { continue }
		info, err := entry.Info()
		if err != nil { return nil, fmt.Errorf("stat recording %q: %w", entry.Name(), err) }
		if !info.Mode().IsRegular() { continue }
		path := filepath.Join(root, entry.Name())
		canonical, err := canonicalPath(path)
		if err != nil { return nil, fmt.Errorf("canonicalize recording %q: %w", entry.Name(), err) }
		if _, ok := references[canonical]; ok { continue }
		if now.Sub(info.ModTime()) < grace { continue }
		orphans = append(orphans, path)
	}
	sort.Strings(orphans)
	return orphans, nil
}
func appendUniquePaths(existing []string, added ...string) []string {
	seen := make(map[string]struct{}, len(existing)+len(added)); out := make([]string, 0, len(existing)+len(added))
	appendPath := func(path string) {
		if strings.TrimSpace(path) == "" { return }
		key := filepath.Clean(path); if canonical, err := canonicalPath(path); err == nil { key = canonical }
		if _, ok := seen[key]; ok { return }; seen[key] = struct{}{}; out = append(out, path)
	}
	for _, path := range existing { appendPath(path) }; for _, path := range added { appendPath(path) }; return out
}
