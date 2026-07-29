package main

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"

	tea "charm.land/bubbletea/v2"

	"github.com/aviorstudio/termcade/internal/engine"
	"github.com/aviorstudio/termcade/internal/plugin"
	"github.com/aviorstudio/termcade/internal/scores"
	"github.com/aviorstudio/termcade/internal/shell"
	"github.com/aviorstudio/termcade/internal/starter"
	"github.com/aviorstudio/termcade/sdk"
)

// discoverGames seeds the starter pack on first run, then loads every
// installed game. The arcade compiles no games in — everything on the menu
// is a .tcade running in the sandbox, bundled or added.
func discoverGames(rt *plugin.Runtime, shape sdk.CellShape) []engine.Registration {
	dir, err := plugin.GamesDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "termcade: games directory unavailable:", err)
		return nil
	}
	if err := starter.Seed(dir); err != nil {
		fmt.Fprintln(os.Stderr, "termcade: starter pack:", err)
	}
	return plugin.Games(rt, dir, nil, shape)
}

// version is stamped by the release build (-X main.version=…). When it
// isn't — `go run …@v0.0.2` compiles without our ldflags — the module
// version Go embeds in the binary is the same truth.
var version = "dev"

func resolveVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "(devel)" && info.Main.Version != "" {
		return info.Main.Version
	}
	return version
}

func main() {
	if runCommand(os.Args[1:]) {
		return
	}

	st, err := scores.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "termcade: score file unavailable:", err)
	}
	shape := pixelShape()

	rt := plugin.NewRuntime(context.Background())
	defer rt.Close()
	games := discoverGames(rt, shape)

	p := tea.NewProgram(shell.New(games, st, shape, newMarketplace(rt, shape)))
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "termcade:", err)
		os.Exit(1)
	}
}

// pixelShape picks the cell subdivision. Quadrants are the default because they
// render in any font; sextants are sharper but need Unicode 13 coverage, so
// opting in is left to whoever knows their terminal.
func pixelShape() sdk.CellShape {
	name, ok := os.LookupEnv("TERMCADE_PIXELS")
	if !ok {
		return sdk.Quadrant
	}
	shape, ok := sdk.LookupShape(name)
	if !ok {
		fmt.Fprintf(os.Stderr, "termcade: unknown TERMCADE_PIXELS %q; using quad\n", name)
		return sdk.Quadrant
	}
	return shape
}
