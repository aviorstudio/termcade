package sdk

import "strings"

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

// LookupShape resolves a shape by name, as accepted by TERMCADE_PIXELS.
func LookupShape(name string) (CellShape, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "half", "halfblock":
		return HalfBlock, true
	case "quad", "quadrant":
		return Quadrant, true
	case "sextant", "six":
		return Sextant, true
	}
	return CellShape{}, false
}
