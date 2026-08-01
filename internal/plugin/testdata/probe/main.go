//go:build wasip1

// A guest that exists to exercise the host, not to be fun.
//
// These tests used to drive Tetris through the sandbox, which meant the host's
// end-to-end coverage depended on a game's rules — and stopped compiling
// entirely when the games moved to their own repository. A probe is the better
// fixture regardless: it touches every ABI export on a schedule the test can
// state exactly, so a failure means the host is wrong rather than that
// somebody retuned a lock delay.
//
// Everything here is deterministic. No clock, no entropy.
package main

import (
	"strconv"

	"github.com/aviorstudio/termcade/sdk"
	"github.com/aviorstudio/termcade/sdk/tcgame"
)

// The playfield the host is told to expect. Small, and even-height as the
// manifest rules require.
const (
	width  = 16
	height = 8

	// GameOverTick is when Update starts reporting the run has ended.
	GameOverTick = 30
)

type probe struct {
	ticks   int
	score   int
	pressed bool
}

func (p *probe) Info() sdk.Info {
	return sdk.Info{ID: "test/probe", Title: "PROBE", PixelW: width, PixelH: height}
}

func (p *probe) Reset() { p.ticks, p.score, p.pressed = 0, 0, false }

// HandleKey scores, so a test can prove a keypress crossed the boundary.
func (p *probe) HandleKey(k sdk.Key) {
	if k == sdk.KeyA {
		p.pressed = true
		p.score += 10
	}
}

// HandleKeyUp clears the flag, so key-up is observable too.
func (p *probe) HandleKeyUp(k sdk.Key) {
	if k == sdk.KeyA {
		p.pressed = false
	}
}

func (p *probe) Update() sdk.Status {
	p.ticks++
	if p.ticks >= GameOverTick {
		return sdk.StatusGameOver
	}
	return sdk.StatusRunning
}

// Draw paints one pixel per elapsed tick along the top row, so the host can
// verify the guest's pixel buffer arrives with exactly the expected content —
// and that Reset really clears it.
func (p *probe) Draw(c *sdk.Canvas) {
	for i := 0; i < p.ticks && i < width; i++ {
		c.SetPixel(i, 0, sdk.White)
	}
	if p.pressed {
		c.SetPixel(0, 1, sdk.White)
	}
}

func (p *probe) Score() int { return p.score }

func (p *probe) HUD() sdk.HUD {
	return sdk.HUD{
		Fields: []sdk.HUDField{{Label: "SCORE", Value: strconv.Itoa(p.score), Accent: true}},
		Hint:   "probe",
	}
}

func init() { tcgame.Register(func() sdk.Game { return &probe{} }) }

func main() {}
