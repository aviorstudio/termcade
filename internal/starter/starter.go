// Package starter seeds a fresh arcade with its bundled games.
//
// The arcade compiles no games in: everything is a .tcade package running in
// the wasm sandbox, exactly like a marketplace game. The starter pack is the
// first-run answer to "then where do the games come from before you have an
// account or a network" — packages embedded in the binary and unpacked into
// the games directory the first time the arcade looks for it.
//
// This is a privileged path, and it is one on purpose. A bundled game does not
// exercise resolve/fetch/verify, so that path can rot without anyone noticing
// until a stranger's game hits it; the marketplace has to earn its own tests.
// What buys back the cost is that a fresh install is playable with no account,
// no network, and no registry — the arcade stops being an empty cabinet
// pointed at a host that may not be answering.
//
// The sources live in the termcade-games repository, not here; only the built
// packages are vendored. The packs are COMMITTED, deliberately breaking the
// no-binaries rule: `go run …@latest` builds from the module zip, which
// contains only committed files, so an uncommitted pack would silently vanish
// from remote installs. They are content assets, rebuilt only when a bundled
// game meaningfully changes (see the go:generate lines, which assume
// termcade-games is checked out beside this repository).
package starter

import (
	"bufio"
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aviorstudio/termcade/manifest"
)

//go:generate sh -c "cd ../.. && go run . dev build ../termcade-games/asteroid && cp ../termcade-games/asteroid/build/asteroid.tcade internal/starter/packs/"
//go:generate sh -c "cd ../.. && go run . dev build ../termcade-games/tetris && cp ../termcade-games/tetris/build/tetris.tcade internal/starter/packs/"
//go:generate sh -c "cd ../.. && go run . dev build ../termcade-games/brickough && cp ../termcade-games/brickough/build/brickough.tcade internal/starter/packs/"

//go:embed packs/*.tcade
var packs embed.FS

// seedMarker records which bundled ids have already been unpacked, one per
// line. Tracking ids rather than a single "seeded" flag means a game added to
// the pack later reaches existing players, without resurrecting one they
// deliberately removed.
const seedMarker = ".starter"

// legacyMarker is the flag file v0.0.2 and v0.0.3 wrote. Those releases
// bundled asteroid and tetris, so its presence means exactly those two have
// been seeded already.
const legacyMarker = ".starter-v1"

var legacySeeded = []string{"aviorstudio/asteroid", "aviorstudio/tetris"}

// Seed unpacks any bundled game the player has not been offered yet.
func Seed(gamesDir string) error {
	seeded, err := readSeeded(gamesDir)
	if err != nil {
		return err
	}
	entries, err := packs.ReadDir("packs")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(gamesDir, 0o755); err != nil {
		return err
	}

	changed := false
	for _, entry := range entries {
		raw, err := packs.ReadFile("packs/" + entry.Name())
		if err != nil {
			return err
		}
		pkg, err := manifest.ReadPackage(raw)
		if err != nil {
			return fmt.Errorf("bundled %s: %w", entry.Name(), err)
		}
		if seeded[pkg.Manifest.Game.ID] {
			continue
		}
		if _, err := pkg.Install(gamesDir); err != nil {
			return fmt.Errorf("seeding %s: %w", pkg.Manifest.Game.ID, err)
		}
		seeded[pkg.Manifest.Game.ID] = true
		changed = true
	}
	if !changed {
		return nil
	}
	return writeSeeded(gamesDir, seeded)
}

// readSeeded reports which ids have already been unpacked, migrating the
// legacy flag file on the way. A missing marker is a fresh arcade, not an
// error.
func readSeeded(gamesDir string) (map[string]bool, error) {
	seeded := map[string]bool{}

	raw, err := os.ReadFile(filepath.Join(gamesDir, seedMarker))
	if err == nil {
		scan := bufio.NewScanner(bytes.NewReader(raw))
		for scan.Scan() {
			if id := strings.TrimSpace(scan.Text()); id != "" {
				seeded[id] = true
			}
		}
		return seeded, scan.Err()
	}
	if !os.IsNotExist(err) {
		return nil, err
	}

	if _, err := os.Stat(filepath.Join(gamesDir, legacyMarker)); err == nil {
		for _, id := range legacySeeded {
			seeded[id] = true
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return seeded, nil
}

func writeSeeded(gamesDir string, seeded map[string]bool) error {
	ids := make([]string, 0, len(seeded))
	for id := range seeded {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	marker := filepath.Join(gamesDir, seedMarker)
	if err := os.WriteFile(marker, []byte(strings.Join(ids, "\n")+"\n"), 0o644); err != nil {
		return fmt.Errorf("marking starter pack installed: %w", err)
	}
	// The legacy flag has been folded into the new marker; leaving it would
	// make a future reader's precedence rules matter.
	_ = os.Remove(filepath.Join(gamesDir, legacyMarker))
	return nil
}
