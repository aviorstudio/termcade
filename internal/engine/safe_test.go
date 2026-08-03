package engine

import (
	"errors"
	"strings"
	"testing"

	"github.com/aviorstudio/termcade/sdk"
)

// panicAt is a game that panics in exactly one method and counts every call
// it receives, so a test can prove the calls after a crash never arrive.
type panicAt struct {
	where  string
	calls  map[string]int
	closed bool
	// closeErr is returned by Close; closePanics makes it panic instead.
	closeErr    error
	closePanics bool
}

func newPanicAt(where string) *panicAt {
	return &panicAt{where: where, calls: map[string]int{}}
}

func (g *panicAt) hit(what string) {
	g.calls[what]++
	if g.where == what {
		panic("boom in " + what)
	}
}

func (g *panicAt) Info() sdk.Info {
	g.hit("Info")
	return sdk.Info{ID: "test/game", Title: "TEST", PixelW: 64, PixelH: 40}
}
func (g *panicAt) Reset()              { g.hit("Reset") }
func (g *panicAt) HandleKey(sdk.Key)   { g.hit("HandleKey") }
func (g *panicAt) HandleKeyUp(sdk.Key) { g.hit("HandleKeyUp") }
func (g *panicAt) Draw(c *sdk.Canvas)  { g.hit("Draw") }
func (g *panicAt) Update() sdk.Status  { g.hit("Update"); return sdk.StatusRunning }
func (g *panicAt) Score() int          { g.hit("Score"); return 7 }
func (g *panicAt) HUD() sdk.HUD        { g.hit("HUD"); return sdk.HUD{} }
func (g *panicAt) Close() error {
	g.closed = true
	if g.closePanics {
		panic("boom in Close")
	}
	return g.closeErr
}

// The claim the README makes: a broken game shows a crash screen instead of
// taking the arcade down. Every method has to hold that, not just the ones
// that happen to be covered elsewhere.
func TestSafeGameContainsAPanicInEveryMethod(t *testing.T) {
	for _, where := range []string{"Reset", "HandleKey", "HandleKeyUp", "Draw", "Update", "Score", "HUD"} {
		t.Run(where, func(t *testing.T) {
			s := Safe(newPanicAt(where))
			if s.Err() != nil {
				t.Fatalf("wrapping alone crashed: %v", s.Err())
			}

			// Whichever method panics, driving the whole surface must not
			// propagate it — this call is what would take the arcade down.
			drive(s)

			if s.Err() == nil {
				t.Fatalf("a panic in %s was swallowed without latching an error", where)
			}
			if got := s.Err().Error(); !strings.Contains(got, where) {
				t.Errorf("error %q does not name the method that failed", got)
			}
		})
	}
}

// A panic during Info is the awkward case: it happens inside Safe itself,
// before the caller holds anything.
func TestSafeGamePanicInInfoIsContained(t *testing.T) {
	s := Safe(newPanicAt("Info"))
	if s.Err() == nil {
		t.Fatal("a panic in Info was swallowed")
	}
	// Info is still answerable, with a zero value rather than a crash, so a
	// caller can render a crash screen without a second check.
	_ = s.Info()
	drive(s)
}

// Latching is what stops a game that panics every frame from burning the
// arcade's time: the first crash wins and nothing reaches the game again.
func TestSafeGameStopsCallingACrashedGame(t *testing.T) {
	g := newPanicAt("Update")
	s := Safe(g)

	s.Update()
	first := s.Err()
	if first == nil {
		t.Fatal("no error latched")
	}
	callsAtCrash := g.calls["Update"]

	for range 5 {
		s.Update()
		s.Draw(nil)
		s.HandleKey(sdk.KeyA)
	}

	if g.calls["Update"] != callsAtCrash {
		t.Errorf("a crashed game was called %d more times", g.calls["Update"]-callsAtCrash)
	}
	if g.calls["Draw"] != 0 || g.calls["HandleKey"] != 0 {
		t.Errorf("calls reached a crashed game: %v", g.calls)
	}
	if s.Err() != first {
		t.Error("a later call replaced the original crash; the first one is the useful one")
	}
}

// Cleanup runs after a crash, which is exactly when it matters — a wasm
// instance still needs releasing.
func TestSafeGameClosesAfterACrash(t *testing.T) {
	g := newPanicAt("Update")
	s := Safe(g)
	s.Update()

	if err := s.Close(); err != nil {
		t.Fatalf("Close after a crash: %v", err)
	}
	if !g.closed {
		t.Error("a crashed game was never closed")
	}
}

func TestSafeGameContainsAPanicInClose(t *testing.T) {
	g := newPanicAt("")
	g.closePanics = true

	err := Safe(g).Close()
	if err == nil {
		t.Fatal("a panic in Close escaped as success")
	}
	if !strings.Contains(err.Error(), "Close") {
		t.Errorf("error %q does not name Close", err)
	}
}

func TestSafeGameReportsACloseError(t *testing.T) {
	sentinel := errors.New("released badly")
	g := newPanicAt("")
	g.closeErr = sentinel

	if err := Safe(g).Close(); !errors.Is(err, sentinel) {
		t.Fatalf("Close returned %v, want the game's own error", err)
	}
}

// A healthy game is left alone: containment must not cost correctness.
func TestSafeGamePassesThroughWhenHealthy(t *testing.T) {
	g := newPanicAt("")
	s := Safe(g)

	if got := s.Info().ID; got != "test/game" {
		t.Errorf("Info().ID = %q", got)
	}
	if got := s.Update(); got != sdk.StatusRunning {
		t.Errorf("Update() = %v, want StatusRunning", got)
	}
	if got := s.Score(); got != 7 {
		t.Errorf("Score() = %d, want 7", got)
	}
	if s.Err() != nil {
		t.Errorf("a healthy game latched %v", s.Err())
	}
}

// drive calls every method that can panic. Draw gets a nil canvas on purpose:
// the wrapper must not care what the game does with it.
func drive(s *SafeGame) {
	s.Reset()
	s.HandleKey(sdk.KeyA)
	s.HandleKeyUp(sdk.KeyA)
	s.Draw(nil)
	s.Update()
	s.Score()
	s.HUD()
}
