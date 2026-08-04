package scores

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// ------------------------------------------------------ runs and the queue --

func TestRecordQueuesTheRunAndKeepsTheHigh(t *testing.T) {
	setupTempConfig(t)
	s, _ := Load()

	if !s.Record("aviorstudio/tetris", 900, "1.2.0", true) {
		t.Error("first run not reported as a new high")
	}
	if s.Record("aviorstudio/tetris", 10, "1.2.0", true) {
		t.Error("a worse run reported as a new high")
	}

	if got := s.High("aviorstudio/tetris"); got != 900 {
		t.Errorf("high = %d, want 900 — a worse run must not lower it", got)
	}
	if got := s.Version("aviorstudio/tetris"); got != "1.2.0" {
		t.Errorf("version = %q, want the package that ran", got)
	}

	pending := s.Pending()
	if len(pending) != 2 {
		t.Fatalf("queued %d runs, want both", len(pending))
	}
	// Both runs happened, so both are owed to the account — the queue is a
	// record of runs, not of high scores.
	if pending[0].Score != 900 || pending[1].Score != 10 {
		t.Errorf("queue is not in the order they were played: %v", pending)
	}
	if pending[0].ID == pending[1].ID {
		t.Error("two runs share an id; the registry would count them as one")
	}
	for _, r := range pending {
		if len(r.ID) < 8 {
			t.Errorf("run id %q is shorter than the registry accepts", r.ID)
		}
	}
}

// Playing must not depend on an account existing, and a run played before
// there is one is exactly what signing in should carry over.
func TestQueueSurvivesASaveAndReload(t *testing.T) {
	setupTempConfig(t)
	s, _ := Load()
	s.Record("aviorstudio/tetris", 900, "1.2.0", true)
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	s2, err := Load()
	if err != nil {
		t.Fatalf("re-Load: %v", err)
	}
	pending := s2.Pending()
	if len(pending) != 1 || pending[0].Score != 900 {
		t.Fatalf("queue did not survive a restart: %v", pending)
	}

	s2.Sent(pending[0].ID)
	if got := s2.Pending(); len(got) != 0 {
		t.Errorf("acknowledged run still queued: %v", got)
	}
	// Acknowledging a run does not touch the score it set.
	if got := s2.High("aviorstudio/tetris"); got != 900 {
		t.Errorf("high = %d after the run was sent, want 900", got)
	}
}

func TestSentIgnoresARunItDoesNotHave(t *testing.T) {
	setupTempConfig(t)
	s, _ := Load()
	s.Record("aviorstudio/tetris", 10, "", true)
	s.Sent("a-run-from-somewhere-else")
	if got := s.Pending(); len(got) != 1 {
		t.Errorf("queue changed: %v", got)
	}
}

func TestQueueIsBounded(t *testing.T) {
	setupTempConfig(t)
	s, _ := Load()
	for i := range maxPending + 50 {
		s.Record("aviorstudio/tetris", i, "", true)
	}
	pending := s.Pending()
	if len(pending) != maxPending {
		t.Fatalf("queue holds %d runs, want it capped at %d", len(pending), maxPending)
	}
	// The oldest go first: their scores are already in the local high, so what
	// is dropped is a play count rather than a record.
	if pending[0].Score != 50 {
		t.Errorf("dropped from the wrong end: oldest kept run scores %d", pending[0].Score)
	}
	if got := s.High("aviorstudio/tetris"); got != maxPending+49 {
		t.Errorf("high = %d; dropping queued runs must not touch it", got)
	}
}

// ------------------------------------------------------------------ merging --

func TestMergeOnlyRaises(t *testing.T) {
	setupTempConfig(t)
	s, _ := Load()
	played := time.Now().UTC().Add(-time.Hour)
	s.Record("aviorstudio/tetris", 900, "1.2.0", true)

	// The account knows less than this machine does.
	s.Merge("aviorstudio/tetris", 10, played.Add(-48*time.Hour))
	if got := s.High("aviorstudio/tetris"); got != 900 {
		t.Errorf("high = %d, want 900 — the account must not lower a local high", got)
	}
	if s.LastPlayed("aviorstudio/tetris").Before(played) {
		t.Error("an older remote timestamp moved last played backwards")
	}

	// And more than it does.
	s.Merge("aviorstudio/tetris", 5000, time.Now().UTC())
	if got := s.High("aviorstudio/tetris"); got != 5000 {
		t.Errorf("high = %d, want the account's 5000", got)
	}
}

// A game only ever played on another machine arrives with nothing local to
// merge into, and has to appear anyway — that is the whole point of syncing.
func TestMergeIntroducesAGameThisMachineHasNotPlayed(t *testing.T) {
	setupTempConfig(t)
	s, _ := Load()
	when := time.Now().UTC().Add(-time.Hour)
	s.Merge("aviorstudio/asteroid", 700, when)

	if got := s.High("aviorstudio/asteroid"); got != 700 {
		t.Errorf("high = %d, want 700", got)
	}
	if got := s.LastPlayed("aviorstudio/asteroid"); !got.Equal(when) {
		t.Errorf("last played = %s, want %s", got, when)
	}
}

// The version describes the package installed HERE. Adopting the account's
// would report a copy as current that this machine does not have.
func TestMergeDoesNotAdoptTheAccountsVersion(t *testing.T) {
	setupTempConfig(t)
	s, _ := Load()
	s.Record("aviorstudio/tetris", 100, "1.0.0", true)
	s.Merge("aviorstudio/tetris", 900, time.Now().UTC())

	if got := s.Version("aviorstudio/tetris"); got != "1.0.0" {
		t.Errorf("version = %q, want the locally installed 1.0.0", got)
	}
}

// Finishing a game used to wipe the timestamp that starting it had just set,
// which took the game straight back off the recently-played list.
func TestFinishingAGameKeepsItRecentlyPlayed(t *testing.T) {
	setupTempConfig(t)
	s, _ := Load()
	s.Touch("aviorstudio/tetris")
	s.Submit("aviorstudio/tetris", 900)

	if s.LastPlayed("aviorstudio/tetris").IsZero() {
		t.Error("a new high score erased when the game was last played")
	}
}

// A v2 file has no queue. It must load as one with an empty queue rather than
// as a failure, and save forward as v3.
func TestV2FileLoadsWithAnEmptyQueue(t *testing.T) {
	dir := setupTempConfig(t)
	path := filepath.Join(dir, "termcade", "scores.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	v2 := `{"version":2,"games":{"aviorstudio/tetris":{"high_score":900}}}`
	if err := os.WriteFile(path, []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := Load()
	if err != nil {
		t.Fatalf("Load v2 file: %v", err)
	}
	if got := s.High("aviorstudio/tetris"); got != 900 {
		t.Errorf("high = %d, want 900", got)
	}
	if got := s.Pending(); len(got) != 0 {
		t.Errorf("queue = %v, want empty", got)
	}

	s.Record("aviorstudio/tetris", 1000, "1.0.0", true)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"version": 3`) {
		t.Errorf("saved file is not v3: %s", raw)
	}
}
