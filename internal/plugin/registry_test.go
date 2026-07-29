package plugin

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aviorstudio/termcade/internal/engine"
	"github.com/aviorstudio/termcade/internal/manifest"
	"github.com/aviorstudio/termcade/sdk"
)

func writeInstall(t *testing.T, gamesDir, author, slug, manifestRaw string, wasm []byte) {
	t.Helper()
	dir := filepath.Join(gamesDir, author, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if manifestRaw != "" {
		if err := os.WriteFile(filepath.Join(dir, manifest.FileName), []byte(manifestRaw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if wasm != nil {
		if err := os.WriteFile(filepath.Join(dir, manifest.WasmName), wasm, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const blasterTOML = `
[game]
id = "acme/blaster"
name = "Blaster"
version = "0.1.0"
[requirements]
abi = 1
width = 40
height = 30
`

func TestRegistryMergesAndFlagsBroken(t *testing.T) {
	gamesDir := t.TempDir()
	fakeWasm := []byte{0x00, 0x61, 0x73, 0x6d}

	writeInstall(t, gamesDir, "acme", "blaster", blasterTOML, fakeWasm)
	// Broken: manifest doesn't parse.
	writeInstall(t, gamesDir, "bad", "toml", "{{{", fakeWasm)
	// Broken: wasm missing.
	writeInstall(t, gamesDir, "bad", "nowasm",
		"[game]\nid = \"bad/nowasm\"\nname = \"NoWasm\"\nversion = \"1.0.0\"\n[requirements]\nabi = 1\nwidth = 8\nheight = 8\n", nil)
	// Broken: future ABI.
	writeInstall(t, gamesDir, "bad", "abi",
		"[game]\nid = \"bad/abi\"\nname = \"Future\"\nversion = \"1.0.0\"\n[requirements]\nabi = 99\nwidth = 8\nheight = 8\n", fakeWasm)

	rt := NewRuntime(context.Background())
	defer rt.Close()
	builtin := engine.Registration{
		Info: sdk.Info{ID: "aviorstudio/tetris", Title: "TETRIS", PixelW: 32, PixelH: 40},
		New:  func() (sdk.Game, error) { return nil, nil },
	}
	games := Games(rt, gamesDir, []engine.Registration{builtin}, sdk.Quadrant)

	if len(games) != 5 {
		t.Fatalf("got %d entries, want 5: %+v", len(games), games)
	}
	if games[0].Info.ID != "aviorstudio/tetris" {
		t.Errorf("builtin not first: %v", games[0].Info.ID)
	}
	if games[1].Info.ID != "acme/blaster" || games[1].Err != nil {
		t.Errorf("installed game not second/healthy: %+v", games[1])
	}
	for _, g := range games[2:] {
		if g.Err == nil {
			t.Errorf("broken entry %s has no error", g.Info.ID)
		}
	}
}

func TestRegistryShadowsBuiltin(t *testing.T) {
	gamesDir := t.TempDir()
	writeInstall(t, gamesDir, "aviorstudio", "tetris",
		"[game]\nid = \"aviorstudio/tetris\"\nname = \"Tetris Deluxe\"\nversion = \"2.0.0\"\n[requirements]\nabi = 1\nwidth = 32\nheight = 40\n",
		[]byte{0x00, 0x61, 0x73, 0x6d})

	rt := NewRuntime(context.Background())
	defer rt.Close()
	builtin := engine.Registration{
		Info: sdk.Info{ID: "aviorstudio/tetris", Title: "TETRIS", PixelW: 32, PixelH: 40},
		New:  func() (sdk.Game, error) { return nil, nil },
	}
	games := Games(rt, gamesDir, []engine.Registration{builtin}, sdk.Quadrant)

	if len(games) != 1 {
		t.Fatalf("got %d entries, want 1 (shadowed): %+v", len(games), games)
	}
	if games[0].Info.Title != "TETRIS DELUXE" {
		t.Errorf("installed game did not shadow builtin: %+v", games[0].Info)
	}
}

func TestRegistryEmptyDirIsFine(t *testing.T) {
	rt := NewRuntime(context.Background())
	defer rt.Close()
	games := Games(rt, filepath.Join(t.TempDir(), "does-not-exist"), nil, sdk.Quadrant)
	if len(games) != 0 {
		t.Errorf("got %d entries from nothing", len(games))
	}
}
