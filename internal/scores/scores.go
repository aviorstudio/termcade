// Package scores persists per-game high scores to the user config directory.
//
// This file is the arcade's own record and stays authoritative for playing:
// the games run with no network and no account, and a high score has to
// survive both. What an account adds is continuity between machines, and that
// is the queue below — runs waiting to reach the registry, and whatever the
// registry knows that this machine did not.
//
// The merge only ever raises a value. A high score set on one machine and a
// higher one set on another both survive; neither client has to be right about
// which is newer, and a sync that never happens costs nothing but the sharing.
package scores

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Entry struct {
	HighScore  int       `json:"high_score"`
	UpdatedAt  time.Time `json:"updated_at"`
	LastPlayed time.Time `json:"last_played,omitzero"`
	// Version is the package version of the last run recorded here. Compared
	// with what the marketplace offers, it is how the library says a local copy
	// has fallen behind.
	Version string `json:"version,omitempty"`
}

// Run is a finished run waiting to reach the account.
//
// ID identifies the RUN, not the request that carries it: it is generated once
// here and reused for every attempt, which is what lets the registry count a
// run once however many times a flaky connection delivers it. A run is dropped
// from the queue only when the registry has acknowledged it.
type Run struct {
	ID        string    `json:"id"`
	Game      string    `json:"game"`
	Score     int       `json:"score"`
	PlayedAt  time.Time `json:"played_at"`
	Completed bool      `json:"completed"`
	Version   string    `json:"version,omitempty"`
}

// version 3: adds the pending-run queue and the per-entry package version.
// Version-2 files load unchanged — an absent queue is an empty one — and are
// rewritten as v3 on the next save.
//
// version 2: game keys are namespaced "author/slug". Version-1 files predate
// plugins, so every bare key in one is a builtin and migrates to "aviorstudio/".
const fileVersion = 3

// maxPending bounds the queue. An arcade played offline for a long time should
// not accumulate an unbounded file, and the oldest runs are the ones whose loss
// costs least — their scores have already been merged into the local high, so
// what is dropped is a play count and a timestamp, not a record.
const maxPending = 500

type file struct {
	Version int              `json:"version"`
	Games   map[string]Entry `json:"games"`
	Pending []Run            `json:"pending,omitempty"`
}

// Store is safe for concurrent use: saves run off the UI loop.
type Store struct {
	mu   sync.Mutex
	path string
	data file
}

func scorePath() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg, "termcade", "scores.json"), nil
}

// Load reads the score file. A missing file yields an empty store and nil
// error; a corrupt file yields an empty store and the parse error so the
// caller can warn — the arcade must never fail to start over a score file.
func Load() (*Store, error) {
	path, err := scorePath()
	if err != nil {
		return &Store{data: file{Version: fileVersion, Games: map[string]Entry{}}}, err
	}
	s := &Store{path: path, data: file{Version: fileVersion, Games: map[string]Entry{}}}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	var parsed file
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return s, fmt.Errorf("parsing %s: %w", path, err)
	}
	if parsed.Games == nil {
		parsed.Games = map[string]Entry{}
	}
	if parsed.Version <= 1 {
		migrated := make(map[string]Entry, len(parsed.Games))
		for k, e := range parsed.Games {
			if !strings.Contains(k, "/") {
				k = "aviorstudio/" + k
			}
			migrated[k] = e
		}
		parsed.Games = migrated
	}
	parsed.Version = fileVersion
	s.data = parsed
	return s, nil
}

// High returns the stored high score for a game, 0 if none.
func (s *Store) High(gameID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Games[gameID].HighScore
}

// Touch records that a game was just played. In-memory; call Save to persist.
func (s *Store) Touch(gameID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.data.Games[gameID]
	e.LastPlayed = time.Now().UTC()
	s.data.Games[gameID] = e
}

// LastPlayed reports when a game was last started, zero if never.
func (s *Store) LastPlayed(gameID string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Games[gameID].LastPlayed
}

// Submit records sc if it beats the stored high; returns true on a new high.
// In-memory only — call Save to persist.
func (s *Store) Submit(gameID string, sc int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.submit(gameID, sc)
}

// submit is Submit without the lock. It updates the high score in place rather
// than replacing the entry: an assignment here used to discard LastPlayed, so
// finishing a game removed it from the recently-played list that starting it
// had just put it on.
func (s *Store) submit(gameID string, sc int) bool {
	e := s.data.Games[gameID]
	if sc <= e.HighScore {
		return false
	}
	e.HighScore = sc
	e.UpdatedAt = time.Now().UTC()
	s.data.Games[gameID] = e
	return true
}

// Record is the end of a run: it updates the local high and queues the run for
// the account. version is the package version that was running, empty for a
// game whose manifest did not say.
//
// The queue is written whether or not anybody is signed in. Signing in later
// is what sends it, which is the point — the arcade is playable with no
// account and no network, and choosing to have one should not begin at zero.
func (s *Store) Record(gameID string, score int, version string, completed bool) (newHigh bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	newHigh = s.submit(gameID, score)
	e := s.data.Games[gameID]
	e.LastPlayed = time.Now().UTC()
	if version != "" {
		e.Version = version
	}
	s.data.Games[gameID] = e

	s.data.Pending = append(s.data.Pending, Run{
		ID:        newRunID(),
		Game:      gameID,
		Score:     score,
		PlayedAt:  e.LastPlayed,
		Completed: completed,
		Version:   version,
	})
	if len(s.data.Pending) > maxPending {
		s.data.Pending = s.data.Pending[len(s.data.Pending)-maxPending:]
	}
	return newHigh
}

// Pending returns the queued runs, oldest first. The slice is a copy: a sync
// runs off the UI loop and must not hold a view into the store.
func (s *Store) Pending() []Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Run, len(s.data.Pending))
	copy(out, s.data.Pending)
	return out
}

// Sent drops a run the registry has acknowledged. Anything it does not
// acknowledge stays queued, which is why a retry has to carry the same run id.
func (s *Store) Sent(runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.data.Pending {
		if r.ID == runID {
			s.data.Pending = append(s.data.Pending[:i], s.data.Pending[i+1:]...)
			return
		}
	}
}

// Merge folds what the account knows into this machine's record. Nothing is
// lowered: a high set here that the registry has not heard about yet survives,
// and so does one set on another machine.
//
// The version is not merged. It describes the package that ran on THIS
// machine, and adopting another's would report a copy as current that is not
// installed here.
func (s *Store) Merge(gameID string, high int, lastPlayed time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.data.Games[gameID]
	if high > e.HighScore {
		e.HighScore = high
		e.UpdatedAt = time.Now().UTC()
	}
	if lastPlayed.After(e.LastPlayed) {
		e.LastPlayed = lastPlayed
	}
	s.data.Games[gameID] = e
}

// Version reports the package version of the last run recorded for a game.
func (s *Store) Version(gameID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Games[gameID].Version
}

// newRunID is a run's identity across every attempt to deliver it. Hex, because
// the registry accepts lowercase letters and digits and there is no reason to
// spend the alphabet.
func newRunID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// crypto/rand does not fail in practice, and a run that cannot be
		// identified is better sent under a time-based id than dropped: the
		// worst case is a duplicate play counted after a retry.
		return fmt.Sprintf("run%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw[:])
}

// Save writes the store atomically (temp file + rename).
func (s *Store) Save() error {
	if s.path == "" {
		return fmt.Errorf("scores: no config path available")
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	s.mu.Lock()
	raw, err := json.MarshalIndent(s.data, "", "  ")
	s.mu.Unlock()
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), "scores-*.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}
