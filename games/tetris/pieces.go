package tetris

import (
	"math/rand"

	"github.com/aviorstudio/termcade/sdk"
)

// Kind is a tetromino type.
type Kind int

const (
	I Kind = iota
	O
	T
	S
	Z
	J
	L
	kindCount
)

var kindColor = [kindCount]sdk.Color{
	I: sdk.Cyan,
	O: sdk.Yellow,
	T: sdk.Purple,
	S: sdk.Green,
	Z: sdk.Red,
	J: sdk.Blue,
	L: sdk.Orange,
}

// Color reports the tetromino's fill color. No piece is black, which is what
// lets a zero Color stand for an empty board cell.
func (k Kind) Color() sdk.Color { return kindColor[k] }

// offsets are the four filled squares of each kind in each of its four
// rotation states, as (col, row) within the piece's own grid. Row 0 is the top,
// matching the board's own downward row order.
//
// These are the Super Rotation System spawn states, so pieces rotate the way
// players expect from modern Tetris.
var offsets = [kindCount][4][4][2]int{
	I: {
		{{0, 1}, {1, 1}, {2, 1}, {3, 1}},
		{{2, 0}, {2, 1}, {2, 2}, {2, 3}},
		{{0, 2}, {1, 2}, {2, 2}, {3, 2}},
		{{1, 0}, {1, 1}, {1, 2}, {1, 3}},
	},
	O: {
		{{1, 0}, {2, 0}, {1, 1}, {2, 1}},
		{{1, 0}, {2, 0}, {1, 1}, {2, 1}},
		{{1, 0}, {2, 0}, {1, 1}, {2, 1}},
		{{1, 0}, {2, 0}, {1, 1}, {2, 1}},
	},
	T: {
		{{1, 0}, {0, 1}, {1, 1}, {2, 1}},
		{{1, 0}, {1, 1}, {2, 1}, {1, 2}},
		{{0, 1}, {1, 1}, {2, 1}, {1, 2}},
		{{1, 0}, {0, 1}, {1, 1}, {1, 2}},
	},
	S: {
		{{1, 0}, {2, 0}, {0, 1}, {1, 1}},
		{{1, 0}, {1, 1}, {2, 1}, {2, 2}},
		{{1, 1}, {2, 1}, {0, 2}, {1, 2}},
		{{0, 0}, {0, 1}, {1, 1}, {1, 2}},
	},
	Z: {
		{{0, 0}, {1, 0}, {1, 1}, {2, 1}},
		{{2, 0}, {1, 1}, {2, 1}, {1, 2}},
		{{0, 1}, {1, 1}, {1, 2}, {2, 2}},
		{{1, 0}, {0, 1}, {1, 1}, {0, 2}},
	},
	J: {
		{{0, 0}, {0, 1}, {1, 1}, {2, 1}},
		{{1, 0}, {2, 0}, {1, 1}, {1, 2}},
		{{0, 1}, {1, 1}, {2, 1}, {2, 2}},
		{{1, 0}, {1, 1}, {0, 2}, {1, 2}},
	},
	L: {
		{{2, 0}, {0, 1}, {1, 1}, {2, 1}},
		{{1, 0}, {1, 1}, {1, 2}, {2, 2}},
		{{0, 1}, {1, 1}, {2, 1}, {0, 2}},
		{{0, 0}, {1, 0}, {1, 1}, {1, 2}},
	},
}

// Piece is a tetromino positioned on the board. X and Y locate the top-left of
// the piece's own grid, so cells may sit at negative coordinates while a piece
// is still entering the well.
type Piece struct {
	Kind Kind
	Rot  int
	X, Y int
}

// Cells returns the four occupied board squares.
func (p Piece) Cells() [4][2]int {
	var out [4][2]int
	for i, off := range offsets[p.Kind][p.Rot&3] {
		out[i] = [2]int{p.X + off[0], p.Y + off[1]}
	}
	return out
}

// kicks are the Super Rotation System wall-kick offsets, tried in order until
// one places the rotated piece somewhere legal. Row deltas are negated from the
// usual published tables because the board counts rows downward.
//
// Indexed [from][to]; only the four adjacent transitions are populated.
var kicksJLSTZ = [4][4][5][2]int{
	0: {
		1: {{0, 0}, {-1, 0}, {-1, -1}, {0, 2}, {-1, 2}},
		3: {{0, 0}, {1, 0}, {1, -1}, {0, 2}, {1, 2}},
	},
	1: {
		0: {{0, 0}, {1, 0}, {1, 1}, {0, -2}, {1, -2}},
		2: {{0, 0}, {1, 0}, {1, 1}, {0, -2}, {1, -2}},
	},
	2: {
		1: {{0, 0}, {-1, 0}, {-1, -1}, {0, 2}, {-1, 2}},
		3: {{0, 0}, {1, 0}, {1, -1}, {0, 2}, {1, 2}},
	},
	3: {
		2: {{0, 0}, {-1, 0}, {-1, 1}, {0, -2}, {-1, -2}},
		0: {{0, 0}, {-1, 0}, {-1, 1}, {0, -2}, {-1, -2}},
	},
}

var kicksI = [4][4][5][2]int{
	0: {
		1: {{0, 0}, {-2, 0}, {1, 0}, {-2, 1}, {1, -2}},
		3: {{0, 0}, {-1, 0}, {2, 0}, {-1, -2}, {2, 1}},
	},
	1: {
		0: {{0, 0}, {2, 0}, {-1, 0}, {2, -1}, {-1, 2}},
		2: {{0, 0}, {-1, 0}, {2, 0}, {-1, -2}, {2, 1}},
	},
	2: {
		1: {{0, 0}, {1, 0}, {-2, 0}, {1, 2}, {-2, -1}},
		3: {{0, 0}, {2, 0}, {-1, 0}, {2, -1}, {-1, 2}},
	},
	3: {
		2: {{0, 0}, {-2, 0}, {1, 0}, {-2, 1}, {1, -2}},
		0: {{0, 0}, {1, 0}, {-2, 0}, {1, 2}, {-2, -1}},
	},
}

// kickTable returns the offsets to try when rotating from one state to another.
// O never needs kicking: it occupies the same squares in every state.
func kickTable(k Kind, from, to int) [5][2]int {
	switch k {
	case O:
		return [5][2]int{}
	case I:
		return kicksI[from&3][to&3]
	default:
		return kicksJLSTZ[from&3][to&3]
	}
}

// bag is a 7-bag randomizer: every kind appears once per seven pieces, which
// bounds droughts without making the order predictable.
type bag struct {
	rng  *rand.Rand
	next []Kind
}

func newBag(rng *rand.Rand) *bag { return &bag{rng: rng} }

func (b *bag) Take() Kind {
	if len(b.next) == 0 {
		b.next = make([]Kind, 0, kindCount)
		for _, i := range b.rng.Perm(int(kindCount)) {
			b.next = append(b.next, Kind(i))
		}
	}
	k := b.next[len(b.next)-1]
	b.next = b.next[:len(b.next)-1]
	return k
}
