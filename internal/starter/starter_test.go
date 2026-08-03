package starter

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aviorstudio/termcade/manifest"
)

// bundled is every id embedded in packs/. Seeding is keyed off the manifests,
// not the file names, so the test reads the same source of truth Seed does.
func bundled(t *testing.T) []string {
	t.Helper()
	entries, err := packs.ReadDir("packs")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, entry := range entries {
		raw, err := packs.ReadFile("packs/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		pkg, err := manifest.ReadPackage(raw)
		if err != nil {
			t.Fatalf("bundled %s does not parse: %v", entry.Name(), err)
		}
		ids = append(ids, pkg.Manifest.Game.ID)
	}
	if len(ids) == 0 {
		t.Fatal("no games are bundled; the starter pack is empty")
	}
	return ids
}

func installed(t *testing.T, gamesDir, id string) bool {
	t.Helper()
	author, slug := filepath.Split(id)
	path := filepath.Join(gamesDir, filepath.Clean(author), slug, manifest.FileName)
	_, err := os.Stat(path)
	return err == nil
}

func TestSeedUnpacksEveryBundledGame(t *testing.T) {
	gamesDir := filepath.Join(t.TempDir(), "games")

	if err := Seed(gamesDir); err != nil {
		t.Fatal(err)
	}
	for _, id := range bundled(t) {
		if !installed(t, gamesDir, id) {
			t.Errorf("%s was not seeded", id)
		}
	}
	if _, err := os.Stat(filepath.Join(gamesDir, seedMarker)); err != nil {
		t.Fatalf("seed marker was not written: %v", err)
	}
}

// A game the player removes stays removed: the marker remembers that it was
// offered, not that it is present.
func TestSeedDoesNotRestoreARemovedGame(t *testing.T) {
	gamesDir := filepath.Join(t.TempDir(), "games")
	if err := Seed(gamesDir); err != nil {
		t.Fatal(err)
	}
	id := bundled(t)[0]
	author, slug := filepath.Split(id)
	dir := filepath.Join(gamesDir, filepath.Clean(author), slug)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	if err := Seed(gamesDir); err != nil {
		t.Fatal(err)
	}
	if installed(t, gamesDir, id) {
		t.Fatalf("removed starter game %s was restored", id)
	}
}

// v0.0.2 and v0.0.3 wrote a bare flag file meaning "asteroid and tetris are
// seeded". Those two must not come back, and anything added to the pack since
// must arrive.
func TestSeedMigratesTheLegacyMarker(t *testing.T) {
	gamesDir := filepath.Join(t.TempDir(), "games")
	if err := os.MkdirAll(gamesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gamesDir, legacyMarker), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Seed(gamesDir); err != nil {
		t.Fatal(err)
	}
	for _, id := range legacySeeded {
		if installed(t, gamesDir, id) {
			t.Errorf("%s was re-seeded over the legacy marker", id)
		}
	}
	for _, id := range bundled(t) {
		if slices.Contains(legacySeeded, id) {
			continue
		}
		if !installed(t, gamesDir, id) {
			t.Errorf("%s was added to the pack but never reached an existing arcade", id)
		}
	}
	if _, err := os.Stat(filepath.Join(gamesDir, legacyMarker)); !os.IsNotExist(err) {
		t.Errorf("legacy marker survived migration: %v", err)
	}
}

// Seeding twice must not rewrite the marker or reinstall anything.
func TestSeedIsIdempotent(t *testing.T) {
	gamesDir := filepath.Join(t.TempDir(), "games")
	if err := Seed(gamesDir); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(gamesDir, seedMarker)
	before, err := os.Stat(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := Seed(gamesDir); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(marker)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Error("a second Seed rewrote the marker")
	}
}
