package sdk

import (
	"math/bits"
	"strings"
)

// CellShape describes how one terminal cell is subdivided into pixels.
//
// A cell is roughly twice as tall as it is wide, so a shape's Cols/Rows also
// fix the pixel aspect ratio: pixels are square only when Rows == 2*Cols.
// Canvas compensates for the rest (see NewCanvas), so games always draw in
// square logical units regardless of the shape in use.
type CellShape struct {
	Name string
	Cols int // pixels across one cell
	Rows int // pixels down one cell
	// Glyphs is indexed by an occupancy bitmask, bit i set meaning pixel i is
	// foreground, numbered row-major from the cell's top-left.
	Glyphs []rune
	// Ink marks glyph sets drawn as strokes rather than tiling blocks: a
	// uniformly foreground-colored cell renders as the full-mask glyph over
	// the canvas background, because no ASCII character fills its cell the
	// way '█' does.
	Ink bool
}

// Shapes, cheapest to sharpest. Every shape renders the same playfield into
// the same number of terminal cells; they differ only in pixels per cell and
// in how new the font has to be.
var (
	// HalfBlock is 1x2 and universally supported.
	HalfBlock = CellShape{
		Name:   "half",
		Cols:   1,
		Rows:   2,
		Glyphs: []rune{' ', '▀', '▄', '█'},
	}

	// Quadrant is 2x2: double the horizontal detail, Unicode 1.0 block
	// elements, so it renders anywhere.
	Quadrant = CellShape{
		Name: "quad",
		Cols: 2,
		Rows: 2,
		Glyphs: []rune{
			' ', '▘', '▝', '▀', '▖', '▌', '▞', '▛',
			'▗', '▚', '▐', '▜', '▄', '▙', '▟', '█',
		},
	}

	// Sextant is 2x3: triple the detail with near-square pixels, but needs
	// Symbols for Legacy Computing (Unicode 13), so older fonts show tofu.
	Sextant = CellShape{
		Name:   "sextant",
		Cols:   2,
		Rows:   3,
		Glyphs: sextantGlyphs(),
	}

	// ASCII is 3x3 with printable-ASCII glyphs only: coverage maps onto a
	// density ramp and single-pixel strokes map onto line characters, so
	// outlines read as line art and fills read as shading. Games draw
	// exactly as they do for every other shape; the ASCII look is entirely
	// the renderer's doing.
	ASCII = CellShape{
		Name:   "ascii",
		Cols:   3,
		Rows:   3,
		Glyphs: asciiGlyphs(),
		Ink:    true,
	}
)

// sextantGlyphs builds the 64-entry mask table. U+1FB00.. enumerates sextant
// masks 1..62 consecutively but skips the four the standard already covers
// elsewhere: empty, full, and the two half-column bars.
func sextantGlyphs() []rune {
	g := make([]rune, 64)
	g[0] = ' '
	g[0b010101] = '▌' // left column
	g[0b101010] = '▐' // right column
	g[63] = '█'
	r := rune(0x1FB00)
	for m := 1; m < 63; m++ {
		if m == 0b010101 || m == 0b101010 {
			continue
		}
		g[m] = r
		r++
	}
	return g
}

// asciiRamp is the classic brightness ramp; a 3x3 cell's popcount 0..9
// indexes it one-to-one.
const asciiRamp = " .:-=+*#%@"

// asciiGlyphs builds the 512-entry mask table: a density ramp by coverage,
// with stroke overrides — a single lit pixel per column whose rows rise,
// fall, or hold reads as a line crossing the cell, and likewise per row.
// Non-monotonic or thick patterns deliberately fall back to the ramp.
func asciiGlyphs() []rune {
	g := make([]rune, 512)
	for m := range g {
		g[m] = rune(asciiRamp[bits.OnesCount16(uint16(m))])
	}
	for m := 1; m < 512; m++ {
		if r0, r1, r2, ok := onePerColumn(m); ok {
			switch {
			case r0 == r1 && r1 == r2:
				g[m] = []rune{'"', '-', '_'}[r0] // flat: top, middle, bottom
			case r0 <= r1 && r1 <= r2:
				g[m] = '\\' // descending left to right
			case r0 >= r1 && r1 >= r2:
				g[m] = '/'
			}
		} else if c0, c1, c2, ok := onePerRow(m); ok {
			switch {
			case c0 == c1 && c1 == c2:
				g[m] = '|' // vertical, any column
			case c0 <= c1 && c1 <= c2:
				g[m] = '\\'
			case c0 >= c1 && c1 >= c2:
				g[m] = '/'
			}
		}
	}
	// Lone pixels sit where the ink sits.
	g[1<<0], g[1<<2] = '`', '\'' // top-left, top-right
	g[1<<6], g[1<<8] = ',', '.'  // bottom-left, bottom-right
	return g
}

// onePerColumn reports each column's single lit row, failing when any column
// is empty or holds more than one pixel. Bit i = row*3 + col.
func onePerColumn(m int) (r0, r1, r2 int, ok bool) {
	rows := [3]int{}
	for c := range 3 {
		switch (m >> c) & 0b001001001 {
		case 1 << 0:
			rows[c] = 0
		case 1 << 3:
			rows[c] = 1
		case 1 << 6:
			rows[c] = 2
		default:
			return 0, 0, 0, false
		}
	}
	return rows[0], rows[1], rows[2], true
}

// onePerRow reports each row's single lit column, failing when any row is
// empty or holds more than one pixel.
func onePerRow(m int) (c0, c1, c2 int, ok bool) {
	cols := [3]int{}
	for r := range 3 {
		switch (m >> (3 * r)) & 0b111 {
		case 0b001:
			cols[r] = 0
		case 0b010:
			cols[r] = 1
		case 0b100:
			cols[r] = 2
		default:
			return 0, 0, 0, false
		}
	}
	return cols[0], cols[1], cols[2], true
}

// LookupShape resolves a shape by name, as accepted by TERMCADE_PIXELS.
func LookupShape(name string) (CellShape, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "half", "halfblock":
		return HalfBlock, true
	case "quad", "quadrant":
		return Quadrant, true
	case "sextant", "six":
		return Sextant, true
	case "ascii":
		return ASCII, true
	}
	return CellShape{}, false
}
