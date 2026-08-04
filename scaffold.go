package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aviorstudio/termcade/manifest"
)

// cmdDevNew scaffolds a playable game project: a paddle-and-ball demo the
// author edits into their own game. The result builds with `termcade dev
// build` as soon as its go.mod resolves.
func cmdDevNew(args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return fmt.Errorf("usage: termcade dev new <author/slug> [dir]")
	}
	id := args[0]
	author, slug, ok := strings.Cut(id, "/")
	if !ok {
		return fmt.Errorf("id must look like author/slug, e.g. you/blaster")
	}

	manifestRaw := fmt.Sprintf(manifestTmpl, id, titleCase(slug))
	if _, err := manifest.Parse([]byte(manifestRaw)); err != nil {
		return err // same rules an install enforces: lowercase [a-z0-9-] etc.
	}

	dir := slug
	if len(args) == 2 {
		dir = args[1]
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s already exists and is not empty", dir)
	}

	pkg := identifier(slug)
	files := map[string]string{
		manifest.FileName:                       manifestRaw,
		"go.mod":                                fmt.Sprintf(goModTmpl, author, slug),
		"game.go":                               fmt.Sprintf(gameTmpl, pkg, pkg, id, strings.ToUpper(slug)),
		filepath.Join("cmd", "wasm", "main.go"): fmt.Sprintf(wasmMainTmpl, author, slug),
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return err
		}
	}

	fmt.Printf(`created %s/
  termcade.toml   identity, playfield, controls
  game.go         the game — start here
  cmd/wasm/       the plugin entrypoint (leave as is)

next:
  cd %s
  go mod tidy     # see go.mod if you develop against a local termcade checkout
  termcade dev build && termcade dev install build/%s.tcade && termcade
`, dir, dir, slug)
	return nil
}

// identifier makes a slug usable as a Go package name: "space-rocks" → "spacerocks".
func identifier(slug string) string {
	s := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, slug)
	if s == "" || s[0] >= '0' && s[0] <= '9' {
		s = "game" + s
	}
	return s
}

func titleCase(slug string) string {
	words := strings.Split(slug, "-")
	for i, w := range words {
		if w != "" {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

const manifestTmpl = `[game]
id          = "%s"
name        = "%s"
version     = "0.1.0"
description = "Describe your game in one line"

[requirements]
abi    = 1  # termcade wasm ABI major version
width  = 64 # logical playfield; must match the game's Info
height = 40

[controls]
"←/→"   = "move paddle"
`

const goModTmpl = `module example.com/%s/%s

go 1.26.2

require github.com/aviorstudio/termcade/sdk v0.0.1

// Developing against a local termcade checkout? Point the sdk there:
// replace github.com/aviorstudio/termcade/sdk => /path/to/termcade/sdk
`

const gameTmpl = `// Package %s is a termcade game: keep the ball up, edit everything.
package %s

import (
	"fmt"
	"math"
	"strings"

	"github.com/aviorstudio/termcade/sdk"
)

var info = sdk.Info{ID: %q, Title: %q, PixelW: 64, PixelH: 40}

const (
	paddleW     = 12.0
	paddleY     = 37.0
	paddleSpeed = 50.0 // units per second
	startLives  = 3
)

type game struct {
	paddleX    float64
	ballX      float64
	ballY      float64
	velX, velY float64
	score      int
	lives      int
	keys       *sdk.KeyTracker
}

// New constructs a fresh game; cmd/wasm registers it with the arcade.
func New() sdk.Game { return &game{} }

func (g *game) Info() sdk.Info { return info }

func (g *game) Reset() {
	g.keys = sdk.NewKeyTracker(sdk.DefaultHoldTicks)
	g.score, g.lives = 0, startLives
	g.paddleX = 32
	g.serve()
}

func (g *game) serve() {
	g.ballX, g.ballY = 32, 8
	g.velX, g.velY = 24, 20
}

func (g *game) HandleKey(k sdk.Key)   { g.keys.Press(k) }
func (g *game) HandleKeyUp(k sdk.Key) { g.keys.Release(k) }

// Update runs exactly sdk.TPS times per simulated second; scale rates by
// sdk.Dt (or count ticks — both are deterministic).
func (g *game) Update() sdk.Status {
	g.keys.Tick()

	if g.keys.Held(sdk.KeyLeft) {
		g.paddleX -= paddleSpeed * sdk.Dt
	}
	if g.keys.Held(sdk.KeyRight) {
		g.paddleX += paddleSpeed * sdk.Dt
	}
	g.paddleX = min(max(g.paddleX, paddleW/2), 64-paddleW/2)

	g.ballX += g.velX * sdk.Dt
	g.ballY += g.velY * sdk.Dt
	if g.ballX < 1 || g.ballX > 63 {
		g.velX = -g.velX
	}
	if g.ballY < 1 {
		g.velY = math.Abs(g.velY)
	}
	if g.velY > 0 && g.ballY >= paddleY-1 && g.ballY <= paddleY+1 &&
		math.Abs(g.ballX-g.paddleX) <= paddleW/2 {
		g.velY = -g.velY
		g.velX *= 1.04 // every save speeds it up a little
		g.velY *= 1.04
		g.score++
	}
	if g.ballY > 40 {
		g.lives--
		if g.lives <= 0 {
			return sdk.StatusGameOver
		}
		g.serve()
	}
	return sdk.StatusRunning
}

// Draw paints onto a cleared canvas in logical units; the arcade handles
// pixels and terminals.
func (g *game) Draw(c *sdk.Canvas) {
	c.FillRectF(g.paddleX-paddleW/2, paddleY, paddleW, 2, sdk.White)
	c.FillCircle(g.ballX, g.ballY, 1, sdk.Yellow)
}

func (g *game) Score() int { return g.score }

func (g *game) HUD() sdk.HUD {
	return sdk.HUD{
		Fields: []sdk.HUDField{
			{Label: "SCORE", Value: fmt.Sprintf("%%06d", g.score), Accent: true},
			{Value: strings.TrimRight(strings.Repeat("● ", g.lives), " "), Accent: true},
		},
		Hint: "←/→ keep the ball up",
	}
}
`

const wasmMainTmpl = `//go:build wasip1

// The termcade plugin entrypoint. You should not need to edit this file.
package main

import (
	"github.com/aviorstudio/termcade/sdk/tcgame"

	game "example.com/%s/%s"
)

func init() { tcgame.Register(game.New) }

func main() {} // never runs; the module is a wasip1 reactor
`
