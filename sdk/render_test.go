package sdk

import (
	"strings"
	"testing"
)

func esc(fg, bg Color) string {
	fr, fgg, fb := fg.RGB()
	br, bgg, bb := bg.RGB()
	return "\x1b[38;2;" + itoa(fr) + ";" + itoa(fgg) + ";" + itoa(fb) +
		";48;2;" + itoa(br) + ";" + itoa(bgg) + ";" + itoa(bb) + "m"
}

func TestRenderGolden(t *testing.T) {
	// 2x2 logical units at quadrant density -> a 4x2 pixel buffer, drawn as
	// one row of two cells.
	c := NewCanvas(2, 2, Black, Quadrant)
	c.Set(0, 0, Red) // fills the top half of cell 0

	got := c.Render()
	want := esc(Red, Black) + "▀" + // cell 0: top two pixels red
		esc(Black, Black) + " " + // cell 1: uniform, drawn as a space
		"\x1b[0m"
	if got != want {
		t.Errorf("Render mismatch:\ngot  %q\nwant %q", got, want)
	}
}

func TestRenderBatchesRuns(t *testing.T) {
	c := NewCanvas(6, 6, Black, Quadrant)
	c.FillRect(0, 0, 6, 6, Blue)
	out := c.Render()
	if n := strings.Count(out, "\n"); n != 2 {
		t.Errorf("expected 3 lines, got %d newlines", n)
	}
	// A uniform fill renders as spaces on a colored background, one escape per
	// line, so a full frame of empty space costs almost nothing.
	if n := strings.Count(out, " "); n != 18 {
		t.Errorf("expected 18 cells, got %d", n)
	}
	if n := strings.Count(out, "\x1b[38;2;"); n != 3 {
		t.Errorf("expected 3 color escapes for uniform canvas, got %d", n)
	}
	if n := strings.Count(out, "\x1b[0m"); n != 3 {
		t.Errorf("expected 3 resets, got %d", n)
	}
}

// TestRenderQuantizesCrowdedCell covers the case a terminal cell cannot
// represent exactly: more than two colors sharing one cell.
func TestRenderQuantizesCrowdedCell(t *testing.T) {
	c := NewCanvas(1, 2, Black, Quadrant) // exactly one cell
	c.SetPixel(0, 0, White)
	c.SetPixel(1, 0, White)
	c.SetPixel(0, 1, Red)
	c.SetPixel(1, 1, Black)

	out := c.Render()
	// White and Black are the most distant pair, so they become fg and bg; the
	// lone Red pixel snaps to whichever it is nearer, and the cell still
	// renders with a single escape and one glyph.
	if n := strings.Count(out, "\x1b[38;2;"); n != 1 {
		t.Errorf("expected 1 escape, got %d in %q", n, out)
	}
	if !strings.Contains(out, esc(White, Black)) {
		t.Errorf("expected the most distant pair as fg/bg, got %q", out)
	}
	glyphs := strings.Count(out, "▀") + strings.Count(out, "▛") +
		strings.Count(out, "█") + strings.Count(out, "▜")
	if glyphs != 1 {
		t.Errorf("expected one quadrant glyph, got %q", out)
	}
}

func TestShapeGlyphTables(t *testing.T) {
	for _, sh := range []CellShape{HalfBlock, Quadrant, Sextant} {
		t.Run(sh.Name, func(t *testing.T) {
			want := 1 << (sh.Cols * sh.Rows)
			if len(sh.Glyphs) != want {
				t.Fatalf("%s has %d glyphs, want %d", sh.Name, len(sh.Glyphs), want)
			}
			if sh.Glyphs[0] != ' ' {
				t.Errorf("%s: empty mask is %q, want a space", sh.Name, sh.Glyphs[0])
			}
			if sh.Glyphs[want-1] != '█' {
				t.Errorf("%s: full mask is %q, want a full block", sh.Name, sh.Glyphs[want-1])
			}
			seen := map[rune]bool{}
			for i, g := range sh.Glyphs {
				if g == 0 {
					t.Fatalf("%s: mask %d has no glyph", sh.Name, i)
				}
				if seen[g] && g != ' ' {
					t.Errorf("%s: glyph %q used for more than one mask", sh.Name, g)
				}
				seen[g] = true
			}
		})
	}
}

// TestSextantColumnGlyphs pins the four masks the Unicode block does not
// encode itself, which the table has to fill in from the block-elements range.
func TestSextantColumnGlyphs(t *testing.T) {
	cases := map[int]rune{0: ' ', 0b010101: '▌', 0b101010: '▐', 63: '█'}
	for mask, want := range cases {
		if got := Sextant.Glyphs[mask]; got != want {
			t.Errorf("sextant mask %06b = %q, want %q", mask, got, want)
		}
	}
}

func itoa(v uint8) string {
	if v == 0 {
		return "0"
	}
	var buf [3]byte
	i := 3
	for v > 0 {
		i--
		buf[i] = '0' + v%10
		v /= 10
	}
	return string(buf[i:])
}
