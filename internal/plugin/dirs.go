package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// GamesDir is where installed games live:
// <user data dir>/termcade/games/<author>/<slug>/.
//
// Go has no os.UserDataDir, so this resolves the platform convention by hand.
// $XDG_DATA_HOME wins everywhere when set, which is also what the tests use.
func GamesDir() (string, error) {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "termcade", "games"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving data dir: %w", err)
	}
	var base string
	switch runtime.GOOS {
	case "darwin":
		base = filepath.Join(home, "Library", "Application Support")
	case "windows":
		if appData := os.Getenv("AppData"); appData != "" {
			base = appData
		} else {
			base = filepath.Join(home, "AppData", "Roaming")
		}
	default:
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "termcade", "games"), nil
}
