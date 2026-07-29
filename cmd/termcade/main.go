package main

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"

	tea "charm.land/bubbletea/v2"

	"github.com/aviorstudio/termcade/games/asteroid"
	"github.com/aviorstudio/termcade/games/tetris"
	"github.com/aviorstudio/termcade/internal/engine"
	"github.com/aviorstudio/termcade/internal/plugin"
	"github.com/aviorstudio/termcade/internal/scores"
	"github.com/aviorstudio/termcade/internal/shell"
	"github.com/aviorstudio/termcade/sdk"
)

// builtin adapts a game constructor into a menu registration. Game packages
// are sdk-only — exactly the shape a third-party game has — so the arcade
// side of the contract lives here, not in them.
func builtin(new func() sdk.Game) engine.Registration {
	info := new().Info()
	return engine.Registration{Info: info, New: func() (sdk.Game, error) { return new(), nil }}
}

// builtins are the games compiled into the arcade. Brickough is deliberately
// absent: it ships as a .tcade package (see games/brickough), keeping the
// install path honestly exercised.
func builtins() []engine.Registration {
	return []engine.Registration{
		builtin(asteroid.New),
		builtin(tetris.New),
	}
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
	games := builtins()
	if dir, err := plugin.GamesDir(); err == nil {
		games = plugin.Games(rt, dir, games, shape)
	} else {
		fmt.Fprintln(os.Stderr, "termcade: installed games unavailable:", err)
	}

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
