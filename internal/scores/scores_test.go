package scores

import (
	"os"
	"path/filepath"
	"testing"
)

func setupTempConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return dir
}

func TestLoadMissingFile(t *testing.T) {
	setupTempConfig(t)
	s, err := Load()
	if err != nil {
		t.Fatalf("Load on missing file: %v", err)
	}
	if got := s.High("brickough"); got != 0 {
		t.Errorf("High on empty store = %d", got)
	}
}

func TestSubmitAndRoundTrip(t *testing.T) {
	setupTempConfig(t)
	s, _ := Load()
	if !s.Submit("brickough", 100) {
		t.Error("first score not reported as new high")
	}
	if s.Submit("brickough", 50) {
		t.Error("lower score reported as new high")
	}
	if s.Submit("brickough", 100) {
		t.Error("equal score reported as new high")
	}
	if !s.Submit("asteroid", 20) {
		t.Error("independent game score not a new high")
	}
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	s2, err := Load()
	if err != nil {
		t.Fatalf("re-Load: %v", err)
	}
	if got := s2.High("brickough"); got != 100 {
		t.Errorf("round-tripped high = %d, want 100", got)
	}
	if got := s2.High("asteroid"); got != 20 {
		t.Errorf("round-tripped high = %d, want 20", got)
	}
}

// TestV1Migration: version-1 files predate plugins, so bare game keys are
// builtins and must migrate to the aviorstudio/ namespace.
func TestV1Migration(t *testing.T) {
	dir := setupTempConfig(t)
	path := filepath.Join(dir, "termcade", "scores.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	v1 := `{"version":1,"games":{"tetris":{"high_score":900},"already/spaced":{"high_score":3}}}`
	if err := os.WriteFile(path, []byte(v1), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load()
	if err != nil {
		t.Fatalf("Load v1 file: %v", err)
	}
	if got := s.High("aviorstudio/tetris"); got != 900 {
		t.Errorf("migrated high = %d, want 900", got)
	}
	if got := s.High("tetris"); got != 0 {
		t.Errorf("bare key survived migration with %d", got)
	}
	if got := s.High("already/spaced"); got != 3 {
		t.Errorf("namespaced key damaged by migration: %d", got)
	}
	// Round-trip: the saved file is v2 and stays migrated.
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s2, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.High("aviorstudio/tetris"); got != 900 {
		t.Errorf("post-save high = %d, want 900", got)
	}
}

func TestLoadCorruptFile(t *testing.T) {
	dir := setupTempConfig(t)
	path := filepath.Join(dir, "termcade", "scores.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load()
	if err == nil {
		t.Error("corrupt file did not return an error")
	}
	if s == nil {
		t.Fatal("corrupt file returned nil store")
	}
	if got := s.High("brickough"); got != 0 {
		t.Errorf("corrupt store High = %d", got)
	}
	// Store still works: submit + save overwrites the corrupt file.
	s.Submit("brickough", 7)
	if err := s.Save(); err != nil {
		t.Fatalf("Save over corrupt file: %v", err)
	}
	s2, err := Load()
	if err != nil {
		t.Fatalf("Load after recovery: %v", err)
	}
	if got := s2.High("brickough"); got != 7 {
		t.Errorf("recovered high = %d, want 7", got)
	}
}
