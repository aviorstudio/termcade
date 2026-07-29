package tetris

import (
	"math/rand"
	"testing"

	"github.com/aviorstudio/termcade/sdk"
)

func newGame(t *testing.T) *game {
	t.Helper()
	g := &game{}
	g.Reset()
	return g
}

// tick advances n frames, stopping early if the run ends.
func tick(g *game, n int) sdk.Status {
	for i := 0; i < n; i++ {
		if s := g.Update(); s == sdk.StatusGameOver {
			return s
		}
	}
	return sdk.StatusRunning
}

func TestResetStartsAPiece(t *testing.T) {
	g := newGame(t)
	if g.dead {
		t.Fatal("game started dead")
	}
	if g.level != 1 || g.lines != 0 || g.score != 0 {
		t.Errorf("fresh game: level=%d lines=%d score=%d", g.level, g.lines, g.score)
	}
	if !g.board.Fits(g.cur) {
		t.Error("spawned piece does not fit the empty well")
	}
}

func TestPieceFallsUnderGravity(t *testing.T) {
	g := newGame(t)
	startY := g.cur.Y
	tick(g, g.gravity())
	if g.cur.Y != startY+1 {
		t.Errorf("piece at Y=%d after one gravity interval, want %d", g.cur.Y, startY+1)
	}
}

func TestGravityAcceleratesWithLevel(t *testing.T) {
	g := newGame(t)
	g.level = 1
	slow := g.gravity()
	g.level = 10
	fast := g.gravity()
	if fast >= slow {
		t.Errorf("level 10 gravity %d is not faster than level 1 gravity %d", fast, slow)
	}
	g.level = 999 // past the table
	if g.gravity() <= 0 {
		t.Errorf("gravity past the table = %d, want a positive interval", g.gravity())
	}
}

func TestHardDropScoresAndLocks(t *testing.T) {
	g := newGame(t)
	before := g.cur
	dist := g.board.DropDistance(before)
	g.HandleKey(sdk.KeyA)

	if g.score != 2*dist {
		t.Errorf("hard drop scored %d, want %d for %d rows", g.score, 2*dist, dist)
	}
	// The piece locked and a new one spawned at the top.
	if g.cur.Y != 0 {
		t.Errorf("no fresh piece after hard drop: Y=%d", g.cur.Y)
	}
	landed := false
	for _, c := range before.Cells() {
		if g.board.At(c[0], c[1]+dist) != 0 {
			landed = true
		}
	}
	if !landed {
		t.Error("dropped piece was not written into the well")
	}
}

func TestHardDropDebounced(t *testing.T) {
	g := newGame(t)
	g.HandleKey(sdk.KeyA)
	first := g.score
	g.HandleKey(sdk.KeyA) // key auto-repeat, same frame
	if g.score != first {
		t.Error("a repeated hard drop fired inside the debounce window")
	}
	tick(g, dropCool)
	g.HandleKey(sdk.KeyA)
	if g.score == first {
		t.Error("hard drop still blocked after the debounce elapsed")
	}
}

func TestLineClearScoresAndLevels(t *testing.T) {
	g := newGame(t)
	// Leave a single gap on the bottom row, then drop an O into it.
	for x := 0; x < boardW; x++ {
		if x != 4 && x != 5 {
			g.board.cells[(boardH-1)*boardW+x] = sdk.Red
			g.board.cells[(boardH-2)*boardW+x] = sdk.Red
		}
	}
	g.cur = Piece{Kind: O, Rot: 0, X: 3, Y: 0} // O sits in columns 4-5
	g.level = 2
	g.HandleKey(sdk.KeyA)

	if g.lines != 2 {
		t.Fatalf("cleared %d lines, want 2", g.lines)
	}
	// Double at level 2, plus the hard-drop distance points.
	if want := lineScore[2] * 2; g.score < want {
		t.Errorf("score %d, want at least the %d line award", g.score, want)
	}
	if g.level != g.lines/linesPerLevel+1 {
		t.Errorf("level %d does not follow from %d lines", g.level, g.lines)
	}
}

func TestToppingOutEndsTheRun(t *testing.T) {
	g := newGame(t)
	// Fill the well solid; the next spawn has nowhere to go.
	for i := range g.board.cells {
		g.board.cells[i] = sdk.Red
	}
	g.spawn()
	if !g.dead {
		t.Fatal("spawning into a full well did not end the run")
	}
	if g.Update() != sdk.StatusGameOver {
		t.Error("Update did not report game over")
	}
}

func TestShiftBlockedByWall(t *testing.T) {
	g := newGame(t)
	g.cur = Piece{Kind: O, Rot: 0, X: -1, Y: 5} // O occupies columns 0-1
	g.shift(-1)
	if g.cur.X != -1 {
		t.Errorf("piece moved through the left wall to X=%d", g.cur.X)
	}
}

func TestDASShiftsOnPressThenRepeats(t *testing.T) {
	g := newGame(t)
	g.cur = Piece{Kind: O, Rot: 0, X: 3, Y: 2}
	startX := g.cur.X

	// First press moves exactly one square, then holds during the DAS pause.
	g.HandleKey(sdk.KeyLeft)
	g.Update()
	if g.cur.X != startX-1 {
		t.Fatalf("X=%d after the initial press, want %d", g.cur.X, startX-1)
	}
	held := g.cur.X
	for i := 0; i < dasDelay-2; i++ {
		g.HandleKey(sdk.KeyLeft)
		g.Update()
	}
	if g.cur.X != held {
		t.Errorf("piece slid during the DAS pause: X=%d, want %d", g.cur.X, held)
	}
	// Past the pause it should slide steadily.
	for i := 0; i < dasRate*3; i++ {
		g.HandleKey(sdk.KeyLeft)
		g.Update()
	}
	if g.cur.X >= held {
		t.Errorf("piece did not auto-shift after the DAS pause: X=%d", g.cur.X)
	}
}

func TestLockDelayGivesGraceThenSets(t *testing.T) {
	g := newGame(t)
	g.cur = Piece{Kind: O, Rot: 0, X: 3, Y: boardH - 2} // resting on the floor
	if !g.grounded() {
		t.Fatal("test setup: piece should be grounded")
	}
	tick(g, lockDelay-1)
	if g.board.At(4, boardH-1) != 0 {
		t.Error("piece locked before the lock delay elapsed")
	}
	tick(g, 2)
	if g.board.At(4, boardH-1) == 0 {
		t.Error("piece never locked after the lock delay")
	}
}

func TestLockResetsAreCapped(t *testing.T) {
	g := newGame(t)
	g.cur = Piece{Kind: O, Rot: 0, X: 3, Y: boardH - 2}
	// Each move refreshes the lock timer, but only up to the cap; past it the
	// piece sets even though the player never stops shuffling.
	peak, locked := 0, false
	for i := 0; i < lockDelay+maxLockResets*4+10; i++ {
		g.shift(1)
		g.shift(-1)
		peak = max(peak, g.lockResets)
		tick(g, 1)
		if g.board.At(4, boardH-1) != 0 {
			locked = true
			break
		}
	}
	if !locked {
		t.Error("piece never locked; shuffling held the well open indefinitely")
	}
	if peak != maxLockResets {
		t.Errorf("lock resets peaked at %d, want the cap %d", peak, maxLockResets)
	}
}

// TestRandomPlayHoldsInvariants drives the game with scripted input and checks
// that nothing panics and the derived state stays consistent throughout.
func TestRandomPlayHoldsInvariants(t *testing.T) {
	g := newGame(t)
	g.rng = rand.New(rand.NewSource(1))
	g.bag = newBag(g.rng)
	c := sdk.NewCanvas(info.PixelW, info.PixelH, sdk.Black, sdk.Quadrant)
	rng := rand.New(rand.NewSource(99))

	lastScore := 0
	for i := 0; i < 20000; i++ {
		switch rng.Intn(20) {
		case 0:
			g.HandleKey(sdk.KeyLeft)
		case 1:
			g.HandleKey(sdk.KeyRight)
		case 2:
			g.HandleKey(sdk.KeyUp)
		case 3:
			g.HandleKey(sdk.KeyDown)
		case 4:
			g.HandleKey(sdk.KeyA)
		}
		status := g.Update()
		g.Draw(c) // must tolerate every reachable state, including game over
		_ = c.Render()

		if g.score < lastScore {
			t.Fatalf("score went backwards at frame %d: %d -> %d", i, lastScore, g.score)
		}
		lastScore = g.score
		if want := g.lines/linesPerLevel + 1; g.level != want {
			t.Fatalf("frame %d: level %d does not follow from %d lines", i, g.level, g.lines)
		}
		if status == sdk.StatusGameOver {
			return
		}
		if !g.board.Fits(g.cur) {
			t.Fatalf("frame %d: live piece overlaps the stack at %+v", i, g.cur)
		}
	}
	t.Log("survived the full run without topping out")
}
