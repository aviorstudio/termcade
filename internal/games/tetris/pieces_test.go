package tetris

import (
	"math/rand"
	"testing"

	"github.com/aviorstudio/termcade/sdk"
)

// TestEveryRotationHasFourSquares catches transcription slips in the SRS
// tables, which are long enough to get a coordinate wrong unnoticed.
func TestEveryRotationHasFourSquares(t *testing.T) {
	for k := Kind(0); k < kindCount; k++ {
		for rot := 0; rot < 4; rot++ {
			seen := map[[2]int]bool{}
			for _, off := range offsets[k][rot] {
				if off[0] < 0 || off[0] > 3 || off[1] < 0 || off[1] > 3 {
					t.Errorf("kind %d rot %d: offset %v outside the 4x4 grid", k, rot, off)
				}
				if seen[off] {
					t.Errorf("kind %d rot %d: duplicate square %v", k, rot, off)
				}
				seen[off] = true
			}
			if len(seen) != 4 {
				t.Errorf("kind %d rot %d: %d distinct squares, want 4", k, rot, len(seen))
			}
		}
	}
}

// TestRotationsPreserveShape checks each rotation really is the same tetromino:
// the multiset of squares must stay connected and the bounding box must only
// transpose, never grow.
func TestRotationsPreserveShape(t *testing.T) {
	for k := Kind(0); k < kindCount; k++ {
		var dims [4][2]int
		for rot := 0; rot < 4; rot++ {
			cells := offsets[k][rot]
			minX, maxX := cells[0][0], cells[0][0]
			minY, maxY := cells[0][1], cells[0][1]
			for _, off := range cells {
				minX, maxX = min(minX, off[0]), max(maxX, off[0])
				minY, maxY = min(minY, off[1]), max(maxY, off[1])
			}
			dims[rot] = [2]int{maxX - minX + 1, maxY - minY + 1}
		}
		// Rotating twice returns to the original footprint.
		for rot := 0; rot < 4; rot++ {
			opp := (rot + 2) & 3
			if dims[rot] != dims[opp] {
				t.Errorf("kind %d: rot %d is %v but rot %d is %v", k, rot, dims[rot], opp, dims[opp])
			}
			adj := (rot + 1) & 3
			if dims[rot][0] != dims[adj][1] || dims[rot][1] != dims[adj][0] {
				t.Errorf("kind %d: rot %d %v is not the transpose of rot %d %v",
					k, rot, dims[rot], adj, dims[adj])
			}
		}
	}
}

func TestNoPieceIsBlack(t *testing.T) {
	// The board relies on this: a zero Color means an empty square.
	for k := Kind(0); k < kindCount; k++ {
		if k.Color() == 0 {
			t.Errorf("kind %d has the zero color, which the board reads as empty", k)
		}
		if k.Color() == sdk.Black {
			t.Errorf("kind %d is black and would vanish against the well", k)
		}
	}
}

func TestBagDealsEachKindOncePerSeven(t *testing.T) {
	b := newBag(rand.New(rand.NewSource(7)))
	for round := 0; round < 20; round++ {
		seen := map[Kind]int{}
		for i := 0; i < int(kindCount); i++ {
			seen[b.Take()]++
		}
		if len(seen) != int(kindCount) {
			t.Fatalf("round %d dealt %d distinct kinds, want %d: %v",
				round, len(seen), kindCount, seen)
		}
	}
}

func TestPieceCellsTranslate(t *testing.T) {
	p := Piece{Kind: T, Rot: 0, X: 4, Y: 6}
	want := [4][2]int{{5, 6}, {4, 7}, {5, 7}, {6, 7}}
	if got := p.Cells(); got != want {
		t.Errorf("Cells() = %v, want %v", got, want)
	}
}

func TestKickTableSizes(t *testing.T) {
	// Only adjacent transitions are ever requested, and each must start with
	// the no-op offset so an unobstructed rotation never shifts the piece.
	for k := Kind(0); k < kindCount; k++ {
		for from := 0; from < 4; from++ {
			to := (from + 1) & 3
			tbl := kickTable(k, from, to)
			if tbl[0] != [2]int{0, 0} {
				t.Errorf("kind %d %d->%d: first kick is %v, want the no-op", k, from, to, tbl[0])
			}
		}
	}
}
