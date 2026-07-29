// Package starter seeds a fresh arcade with its bundled games.
//
// The arcade compiles no games in: everything is a .tcade package running in
// the wasm sandbox, exactly like a marketplace game. The starter pack is the
// first-run answer to "then where do the games come from before you have an
// account or a network" — packages embedded in the binary and unpacked into
// the games directory the first time the arcade looks for it.
//
// The packs are COMMITTED, deliberately breaking the no-binaries rule:
// `go run …@latest` builds from the module zip, which contains only
// committed files, so an uncommitted pack would silently vanish from
// remote installs. They are content assets, rebuilt only when a bundled
// game meaningfully changes (see the go:generate lines).
package starter

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aviorstudio/termcade/internal/manifest"
)

//go:generate sh -c "cd ../.. && go run . dev build games/asteroid && cp games/asteroid/build/asteroid.tcade internal/starter/packs/"
//go:generate sh -c "cd ../.. && go run . dev build games/tetris && cp games/tetris/build/tetris.tcade internal/starter/packs/"

//go:embed packs/*.tcade
var packs embed.FS

const seedMarker = ".starter-v1"

// Seed unpacks the bundled games once, including for users whose games
// directory predates the starter pack. The marker is written only after every
// package installs successfully, so later removal of a starter game sticks.
func Seed(gamesDir string) error {
	marker := filepath.Join(gamesDir, seedMarker)
	if _, err := os.Stat(marker); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	entries, err := packs.ReadDir("packs")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(gamesDir, 0o755); err != nil {
		return err
	}
	for _, entry := range entries {
		raw, err := packs.ReadFile("packs/" + entry.Name())
		if err != nil {
			return err
		}
		pkg, err := manifest.ReadPackage(raw)
		if err != nil {
			return fmt.Errorf("bundled %s: %w", entry.Name(), err)
		}
		if _, err := pkg.Install(gamesDir); err != nil {
			return fmt.Errorf("seeding %s: %w", pkg.Manifest.Game.ID, err)
		}
	}
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		return fmt.Errorf("marking starter pack installed: %w", err)
	}
	return nil
}
