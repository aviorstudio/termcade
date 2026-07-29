// Package scores persists per-game high scores to the user config directory.
package scores

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Entry struct {
	HighScore int       `json:"high_score"`
	UpdatedAt time.Time `json:"updated_at"`
}

// version 2: game keys are namespaced "author/slug". Version-1 files predate
// plugins, so every bare key in one is a builtin and migrates to "aviorstudio/".
const fileVersion = 2

type file struct {
	Version int              `json:"version"`
	Games   map[string]Entry `json:"games"`
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

// Submit records sc if it beats the stored high; returns true on a new high.
// In-memory only — call Save to persist.
func (s *Store) Submit(gameID string, sc int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sc <= s.data.Games[gameID].HighScore {
		return false
	}
	s.data.Games[gameID] = Entry{HighScore: sc, UpdatedAt: time.Now().UTC()}
	return true
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
