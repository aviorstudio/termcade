package engine

import (
	"fmt"
	"io"

	"github.com/aviorstudio/termcade/sdk"
)

// SafeGame wraps a Game so a panic in game code cannot take down the arcade:
// the first panic latches an error, every later call becomes a no-op, and the
// shell inspects Err to show a crash screen instead of dying. Builtin and
// plugin games get the same containment.
type SafeGame struct {
	g    sdk.Game
	info sdk.Info
	err  error
}

// Safe wraps g. The game's Info is captured eagerly so identity and canvas
// size survive a later crash.
func Safe(g sdk.Game) *SafeGame {
	s := &SafeGame{g: g}
	s.do("Info", func() { s.info = g.Info() })
	return s
}

func (s *SafeGame) do(what string, f func()) {
	if s.err != nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			s.err = fmt.Errorf("game %s panicked: %v", what, r)
		}
	}()
	f()
}

// Err reports the latched crash, nil while the game is healthy.
func (s *SafeGame) Err() error { return s.err }

func (s *SafeGame) Info() sdk.Info        { return s.info }
func (s *SafeGame) Reset()                { s.do("Reset", s.g.Reset) }
func (s *SafeGame) HandleKey(k sdk.Key)   { s.do("HandleKey", func() { s.g.HandleKey(k) }) }
func (s *SafeGame) HandleKeyUp(k sdk.Key) { s.do("HandleKeyUp", func() { s.g.HandleKeyUp(k) }) }
func (s *SafeGame) Draw(c *sdk.Canvas)    { s.do("Draw", func() { s.g.Draw(c) }) }

func (s *SafeGame) Update() sdk.Status {
	var st sdk.Status
	s.do("Update", func() { st = s.g.Update() })
	return st
}

func (s *SafeGame) Score() int {
	var n int
	s.do("Score", func() { n = s.g.Score() })
	return n
}

func (s *SafeGame) HUD() sdk.HUD {
	var h sdk.HUD
	s.do("HUD", func() { h = s.g.HUD() })
	return h
}

// Close releases the underlying game's resources if it holds any (a wasm
// instance, for example). It runs even after a crash — that is exactly when
// cleanup matters most.
func (s *SafeGame) Close() (err error) {
	c, ok := s.g.(io.Closer)
	if !ok {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("game Close panicked: %v", r)
		}
	}()
	return c.Close()
}
