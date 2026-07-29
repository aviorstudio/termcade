//go:build wasip1

// A hostile guest for the watchdog test: Update never returns.
package main

import (
	"github.com/aviorstudio/termcade/sdk"
	"github.com/aviorstudio/termcade/sdk/tcgame"
)

type hang struct{}

func (hang) Info() sdk.Info      { return sdk.Info{ID: "test/hang", Title: "HANG", PixelW: 8, PixelH: 8} }
func (hang) Reset()              {}
func (hang) HandleKey(sdk.Key)   {}
func (hang) HandleKeyUp(sdk.Key) {}
func (hang) Update() sdk.Status {
	for {
	}
}
func (hang) Draw(*sdk.Canvas) {}
func (hang) Score() int       { return 0 }
func (hang) HUD() sdk.HUD     { return sdk.HUD{} }

func init() { tcgame.Register(func() sdk.Game { return hang{} }) }

func main() {}
