package tetris

import "github.com/aviorstudio/termcade/sdk"

const (
	boardW = 10
	boardH = 20
)

// Board is the well. A zero Color means an empty square: no tetromino is
// black, so the zero value doubles as "empty".
type Board struct {
	W, H  int
	cells []sdk.Color
}

func NewBoard(w, h int) *Board {
	return &Board{W: w, H: h, cells: make([]sdk.Color, w*h)}
}

func (b *Board) Clear() {
	for i := range b.cells {
		b.cells[i] = 0
	}
}

func (b *Board) At(x, y int) sdk.Color {
	if x < 0 || x >= b.W || y < 0 || y >= b.H {
		return 0
	}
	return b.cells[y*b.W+x]
}

// blocked reports whether a square cannot hold part of a piece. The walls and
// floor block; above the ceiling is open, so pieces can enter the well.
func (b *Board) blocked(x, y int) bool {
	if x < 0 || x >= b.W || y >= b.H {
		return true
	}
	if y < 0 {
		return false
	}
	return b.cells[y*b.W+x] != 0
}

// Fits reports whether a piece can occupy its current position.
func (b *Board) Fits(p Piece) bool {
	for _, c := range p.Cells() {
		if b.blocked(c[0], c[1]) {
			return false
		}
	}
	return true
}

// Lock writes a piece into the well. Squares above the ceiling are dropped,
// which is how a piece that locks while still entering ends the run.
func (b *Board) Lock(p Piece) {
	col := p.Kind.Color()
	for _, c := range p.Cells() {
		x, y := c[0], c[1]
		if x < 0 || x >= b.W || y < 0 || y >= b.H {
			continue
		}
		b.cells[y*b.W+x] = col
	}
}

// ClearLines removes every full row and settles the rows above, returning how
// many were cleared.
func (b *Board) ClearLines() int {
	dst := b.H - 1
	cleared := 0
	for src := b.H - 1; src >= 0; src-- {
		if b.rowFull(src) {
			cleared++
			continue
		}
		if dst != src {
			copy(b.cells[dst*b.W:(dst+1)*b.W], b.cells[src*b.W:(src+1)*b.W])
		}
		dst--
	}
	for ; dst >= 0; dst-- {
		clear(b.cells[dst*b.W : (dst+1)*b.W])
	}
	return cleared
}

func (b *Board) rowFull(y int) bool {
	for x := 0; x < b.W; x++ {
		if b.cells[y*b.W+x] == 0 {
			return false
		}
	}
	return true
}

// Rotate turns a piece one quarter-turn and applies the first wall kick that
// lands it somewhere legal. It reports whether the rotation succeeded.
func (b *Board) Rotate(p Piece, dir int) (Piece, bool) {
	to := (p.Rot + dir) & 3
	rotated := Piece{Kind: p.Kind, Rot: to, X: p.X, Y: p.Y}
	for _, k := range kickTable(p.Kind, p.Rot, to) {
		cand := rotated
		cand.X += k[0]
		cand.Y += k[1]
		if b.Fits(cand) {
			return cand, true
		}
	}
	return p, false
}

// DropDistance counts how many rows a piece can fall before it would land.
func (b *Board) DropDistance(p Piece) int {
	n := 0
	for {
		next := p
		next.Y++
		if !b.Fits(next) {
			return n
		}
		p = next
		n++
	}
}
