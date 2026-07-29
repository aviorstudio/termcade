# Writing a termcade game

Games are ordinary Go packages built against the termcade SDK, compiled to
WebAssembly, and packaged as a `.tcade` file anyone can install with
`termcade add`. Installed games run sandboxed: no filesystem, no network,
no environment — a clock, entropy, and a pixel buffer.

## Quickstart

The arcade is the dev kit — scaffold a working game and play it:

```sh
termcade dev new you/mygame
cd mygame
go mod tidy
termcade dev build && termcade add build/mygame.tcade && termcade
```

`dev new` generates a playable paddle-and-ball demo; edit `game.go` from
there. (Working against an unpublished termcade checkout? Uncomment the
`replace` line in the generated go.mod.) The rest of this document explains
what the scaffold gave you.

## By hand

```sh
mkdir mygame && cd mygame
go mod init example.com/you/mygame
go get github.com/aviorstudio/termcade/sdk
```

Implement `sdk.Game`:

```go
package mygame

import "github.com/aviorstudio/termcade/sdk"

var info = sdk.Info{ID: "you/mygame", Title: "MYGAME", PixelW: 64, PixelH: 40}

type game struct {
	x    float64
	keys *sdk.KeyTracker
}

func New() sdk.Game { return &game{} }

func (g *game) Info() sdk.Info { return info }

func (g *game) Reset() {
	g.x = 32
	g.keys = sdk.NewKeyTracker(sdk.DefaultHoldTicks)
}

func (g *game) HandleKey(k sdk.Key)   { g.keys.Press(k) }
func (g *game) HandleKeyUp(k sdk.Key) { g.keys.Release(k) }

func (g *game) Update() sdk.Status {
	g.keys.Tick()
	if g.keys.Held(sdk.KeyRight) {
		g.x += 30 * sdk.Dt // units per second × the fixed tick
	}
	return sdk.StatusRunning
}

func (g *game) Draw(c *sdk.Canvas) {
	c.FillRectF(g.x-6, 37, 12, 2, sdk.White)
}

func (g *game) Score() int { return 0 }

func (g *game) HUD() sdk.HUD {
	return sdk.HUD{Fields: []sdk.HUDField{{Label: "SCORE", Value: "000000", Accent: true}}}
}
```

Add the wasm entrypoint at `cmd/wasm/main.go`:

```go
//go:build wasip1

package main

import (
	"github.com/aviorstudio/termcade/sdk/tcgame"

	mygame "example.com/you/mygame"
)

func init() { tcgame.Register(mygame.New) }

func main() {} // never runs; the module is a wasip1 reactor
```

Add a `termcade.toml` (see [packaging.md](packaging.md)), then:

```sh
termcade dev build          # → build/mygame.tcade
termcade add build/mygame.tcade
termcade                    # your game is on the menu
```

## The contract

- **Timing** — `Update` is called exactly `sdk.TPS` (60) times per simulated
  second, always one fixed tick. Count ticks or multiply rates by `sdk.Dt`;
  both are deterministic. There is no variable dt.
- **Drawing** — `Draw` receives a cleared canvas of `Info.PixelW × PixelH`
  logical units (height must be even). Draw in logical units; the canvas
  rasterizes to whatever pixel density the player's terminal uses. The
  terminal footprint is `PixelW` columns × `PixelH/2` rows.
- **Input** — the vocabulary is `KeyLeft/Right/Up/Down`, `KeyA` (space/z),
  `KeyB` (x), `KeyStart` (enter). Esc belongs to the arcade (pause). Terminals
  that report key releases call `HandleKeyUp`; for the rest, use
  `sdk.KeyTracker`, which falls back to auto-repeat timing automatically.
- **HUD** — return structured fields, not styled text. The arcade owns
  styling and strips control characters.
- **Crashes** — a panic, a trap, or an `Update` that exceeds the watchdog
  budget (500ms) kills your game's instance, never the arcade. The player
  sees a crash screen.
- **Identity** — your `Info.ID` must equal the manifest id (`author/slug`).
  The host trusts the manifest, and checks that your playfield matches it.

## Local iteration without wasm

Nothing about the SDK requires wasm — a game is plain Go. For fast iteration,
write ordinary Go tests against your game (see `games/brickough` in the
termcade repo for a worked example: a complete sdk-only game with a physics
test suite, shipped purely as a `.tcade`).
