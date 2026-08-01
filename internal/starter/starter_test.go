package starter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aviorstudio/termcade/manifest"
)

func TestSeedMigratesExistingGamesDirectoryOnlyOnce(t *testing.T) {
	gamesDir := filepath.Join(t.TempDir(), "games")
	if err := os.MkdirAll(filepath.Join(gamesDir, "aviorstudio"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Seed(gamesDir); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"asteroid", "tetris"} {
		path := filepath.Join(gamesDir, "aviorstudio", slug, manifest.FileName)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s was not seeded: %v", slug, err)
		}
	}
	if _, err := os.Stat(filepath.Join(gamesDir, seedMarker)); err != nil {
		t.Fatalf("seed marker was not written: %v", err)
	}

	asteroidDir := filepath.Join(gamesDir, "aviorstudio", "asteroid")
	if err := os.RemoveAll(asteroidDir); err != nil {
		t.Fatal(err)
	}
	if err := Seed(gamesDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(asteroidDir); !os.IsNotExist(err) {
		t.Fatalf("removed starter game was restored: %v", err)
	}
}
