package tetris

import (
	"testing"

	"github.com/aviorstudio/termcade/sdk"
)

// fill marks a run of squares in a row, so tests can describe a well compactly.
func fill(b *Board, y int, xs ...int) {
	for _, x := range xs {
		b.cells[y*b.W+x] = sdk.Red
	}
}

func fullRow(b *Board, y int, except ...int) {
	skip := map[int]bool{}
	for _, x := range except {
		skip[x] = true
	}
	for x := 0; x < b.W; x++ {
		if !skip[x] {
			b.cells[y*b.W+x] = sdk.Green
		}
	}
}

func TestBlockedWallsAndCeiling(t *testing.T) {
	b := NewBoard(boardW, boardH)
	cases := []struct {
		x, y int
		want bool
		why  string
	}{
		{-1, 5, true, "left wall"},
		{boardW, 5, true, "right wall"},
		{3, boardH, true, "floor"},
		{3, -1, false, "above the ceiling is open so pieces can enter"},
		{3, 5, false, "empty square"},
	}
	for _, c := range cases {
		if got := b.blocked(c.x, c.y); got != c.want {
			t.Errorf("blocked(%d,%d) = %v, want %v (%s)", c.x, c.y, got, c.want, c.why)
		}
	}
}

func TestClearLinesCompacts(t *testing.T) {
	b := NewBoard(boardW, boardH)
	fill(b, boardH-4, 0, 1) // survivor, should end up resting lower
	fullRow(b, boardH-3)    // cleared
	fill(b, boardH-2, 7)    // survivor
	fullRow(b, boardH-1)    // cleared

	if n := b.ClearLines(); n != 2 {
		t.Fatalf("ClearLines = %d, want 2", n)
	}
	// The two survivors settle to the bottom, keeping their relative order.
	if b.At(7, boardH-1) == 0 {
		t.Error("lower survivor did not settle to the floor")
	}
	if b.At(0, boardH-2) == 0 || b.At(1, boardH-2) == 0 {
		t.Error("upper survivor did not settle above the lower one")
	}
	for x := 0; x < boardW; x++ {
		if b.At(x, boardH-3) != 0 {
			t.Errorf("row above the settled stack not empty at x=%d", x)
		}
	}
}

func TestClearLinesFullBoard(t *testing.T) {
	b := NewBoard(boardW, boardH)
	for y := 0; y < boardH; y++ {
		fullRow(b, y)
	}
	if n := b.ClearLines(); n != boardH {
		t.Fatalf("ClearLines = %d, want %d", n, boardH)
	}
	for y := 0; y < boardH; y++ {
		for x := 0; x < boardW; x++ {
			if b.At(x, y) != 0 {
				t.Fatalf("square (%d,%d) survived a full clear", x, y)
			}
		}
	}
}

func TestLockDropsSquaresAboveCeiling(t *testing.T) {
	b := NewBoard(boardW, boardH)
	// A piece still entering the well: part of it sits above row 0.
	p := Piece{Kind: T, Rot: 0, X: 3, Y: -1}
	b.Lock(p) // must not panic or wrap around
	for x := 0; x < boardW; x++ {
		if b.At(x, boardH-1) != 0 {
			t.Fatalf("off-board square wrapped into the floor at x=%d", x)
		}
	}
}

func TestDropDistance(t *testing.T) {
	b := NewBoard(boardW, boardH)
	p := Piece{Kind: O, X: spawnX, Y: 0}
	// O occupies rows 0-1 of its grid, so its lowest square is at Y+1.
	if got, want := b.DropDistance(p), boardH-2; got != want {
		t.Fatalf("DropDistance on an empty well = %d, want %d", got, want)
	}
	fullRow(b, boardH-1)
	if got, want := b.DropDistance(p), boardH-3; got != want {
		t.Fatalf("DropDistance over one filled row = %d, want %d", got, want)
	}
}

// TestRotateKicksOffWall is the payoff of the SRS tables: a piece flush against
// the wall still rotates, by shifting away from it.
func TestRotateKicksOffWall(t *testing.T) {
	b := NewBoard(boardW, boardH)
	// Vertical I hard against the left wall: its squares sit in column X+1.
	p := Piece{Kind: I, Rot: 1, X: -1, Y: 5}
	if !b.Fits(p) {
		t.Fatal("test setup: vertical I should fit against the left wall")
	}
	got, ok := b.Rotate(p, 1)
	if !ok {
		t.Fatal("rotation against the wall failed; kicks did not apply")
	}
	if !b.Fits(got) {
		t.Error("kicked rotation landed in an illegal position")
	}
	if got.X == p.X {
		t.Error("expected the kick to shift the piece away from the wall")
	}
}

func TestRotateBlockedReturnsOriginal(t *testing.T) {
	b := NewBoard(boardW, boardH)
	p := Piece{Kind: T, Rot: 0, X: 3, Y: 5}
	// Fill the entire well except the four squares the piece already occupies,
	// so no kick anywhere on the board can succeed.
	for y := 0; y < boardH; y++ {
		fullRow(b, y)
	}
	for _, c := range p.Cells() {
		b.cells[c[1]*b.W+c[0]] = 0
	}
	if !b.Fits(p) {
		t.Fatal("test setup: the piece should fit in its own pocket")
	}
	got, ok := b.Rotate(p, 1)
	if ok {
		t.Fatalf("rotation succeeded in a sealed pocket: %+v", got)
	}
	if got != p {
		t.Errorf("failed rotation returned %+v, want the original %+v", got, p)
	}
}

func TestORotationIsIdentity(t *testing.T) {
	b := NewBoard(boardW, boardH)
	p := Piece{Kind: O, Rot: 0, X: 3, Y: 5}
	before := p.Cells()
	got, ok := b.Rotate(p, 1)
	if !ok {
		t.Fatal("O failed to rotate")
	}
	if got.Cells() != before {
		t.Errorf("O moved when rotated: %v -> %v", before, got.Cells())
	}
}
