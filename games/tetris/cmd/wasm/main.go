//go:build wasip1

// The tetris wasm guest: the same game the arcade compiles in, packaged
// the way a third-party marketplace game would be.
package main

import (
	"github.com/aviorstudio/termcade/games/tetris"
	"github.com/aviorstudio/termcade/sdk/tcgame"
)

func init() { tcgame.Register(tetris.New) }

func main() {} // never runs; the module is a wasip1 reactor
