package privacy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type recordingRepoStub struct { *repoStub; references []string; referenceErr error }
func (r *recordingRepoStub) ReferencedVoiceMemoPaths(context.Context) ([]string, error) {
	if r.referenceErr != nil { return nil, r.referenceErr }
	return append([]string(nil), r.references...), nil
}
func writeRecordingFixture(t *testing.T, path string, modified time.Time) {
	t.Helper(); if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil { t.Fatal(err) }; if err := os.Chtimes(path, modified, modified); err != nil { t.Fatal(err) }
}
func TestApplyRetentionReportsOnlyOldUnreferencedRegularWAVs(t *testing.T) {
	now := time.Date(2026, 8, 14, 6, 0, 0, 0, time.UTC); dir := t.TempDir()
	kept := filepath.Join(dir, "kept.wav"); orphan := filepath.Join(dir, "orphan.WAV"); fresh := filepath.Join(dir, "fresh.wav"); nonAudio := filepath.Join(dir, "notes.txt")
	writeRecordingFixture(t, kept, now.Add(-2*time.Hour)); writeRecordingFixture(t, orphan, now.Add(-2*time.Hour)); writeRecordingFixture(t, fresh, now.Add(-30*time.Minute)); writeRecordingFixture(t, nonAudio, now.Add(-2*time.Hour))
	repo := &recordingRepoStub{repoStub: &repoStub{}, references: []string{kept}}; service := New(repo); service.recordingsDir = dir; service.orphanGrace = time.Hour; service.now = func() time.Time { return now }
	report, err := service.ApplyRetention(context.Background()); if err != nil { t.Fatal(err) }
	if len(report.OrphanPaths) != 1 || filepath.Clean(report.OrphanPaths[0]) != filepath.Clean(orphan) { t.Fatalf("orphan paths=%v; want only %s", report.OrphanPaths, orphan) }
}
func TestApplyRetentionIgnoresSymlinkedRecording(t *testing.T) {
	now := time.Date(2026, 8, 14, 6, 0, 0, 0, time.UTC); dir := t.TempDir(); target := filepath.Join(t.TempDir(), "outside.wav"); writeRecordingFixture(t, target, now.Add(-2*time.Hour)); link := filepath.Join(dir, "link.wav")
	if err := os.Symlink(target, link); err != nil { t.Skipf("symlink unavailable: %v", err) }
	repo := &recordingRepoStub{repoStub: &repoStub{}}; service := New(repo); service.recordingsDir = dir; service.orphanGrace = time.Hour; service.now = func() time.Time { return now }
	report, err := service.ApplyRetention(context.Background()); if err != nil { t.Fatal(err) }; if len(report.OrphanPaths) != 0 { t.Fatalf("symlink must never be scheduled for deletion: %v", report.OrphanPaths) }
}
func TestApplyRetentionFailsClosedWhenRecordingReferencesCannotBeRead(t *testing.T) {
	repo := &recordingRepoStub{repoStub: &repoStub{}, referenceErr: errors.New("database unavailable")}; service := New(repo); service.recordingsDir = t.TempDir(); if _, err := service.ApplyRetention(context.Background()); err == nil { t.Fatal("reference lookup failure must stop orphan reconciliation") }
}
func TestAppendUniquePathsDeduplicatesEquivalentNames(t *testing.T) {
	dir := t.TempDir(); first := filepath.Join(dir, "memo.wav"); second := filepath.Join(dir, ".", "memo.wav"); paths := appendUniquePaths([]string{first}, second); if len(paths) != 1 { t.Fatalf("paths=%v; want one canonical recording", paths) }
}
