// Package settings persists small player preferences to the config dir.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Settings struct {
	// Pixels is the preferred CellShape name (sdk.LookupShape). Empty means
	// no preference saved; TERMCADE_PIXELS overrides it for a run without
	// ever writing here.
	Pixels string `json:"pixels,omitempty"`
}

func path() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg, "termcade", "settings.json"), nil
}

// Load returns the saved preferences; a missing file is the zero value.
func Load() (Settings, error) {
	p, err := path()
	if err != nil {
		return Settings{}, err
	}
	raw, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, err
	}
	var s Settings
	if err := json.Unmarshal(raw, &s); err != nil {
		return Settings{}, fmt.Errorf("parsing %s: %w", p, err)
	}
	return s, nil
}

func (s Settings) Save() error {
	p, err := path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, raw, 0o644)
}
