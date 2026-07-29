package sdk

import (
	"strconv"
	"strings"
)

// maxCellPixels bounds the per-cell scratch buffer across all shapes.
const maxCellPixels = 6

// Render converts the canvas to terminal output, one line per cell row.
//
// A cell holds Cols*Rows pixels but a terminal cell carries only two colors, so
// cells with more than two distinct colors are quantized to the two furthest
// apart and each pixel snaps to the nearer. Escapes are emitted only when the
// (fg,bg) pair changes along a row, and every line ends with a reset so lines
// stay safe to splice independently.
func (c *Canvas) Render() string {
	sh := c.shape
	cellsW := c.fw / sh.Cols
	cellsH := c.fh / sh.Rows

	var b strings.Builder
	// Rough capacity: a glyph per cell, plus an escape every few cells.
	b.Grow(cellsW*cellsH*6 + cellsH*8)

	var buf [maxCellPixels]Color
	var sgr [40]byte
	for cy := 0; cy < cellsH; cy++ {
		if cy > 0 {
			b.WriteByte('\n')
		}
		var curFg, curBg Color
		havePrev := false
		for cx := 0; cx < cellsW; cx++ {
			fg, bg, mask := c.cell(cx, cy, buf[:])
			if !havePrev || fg != curFg || bg != curBg {
				b.Write(appendSGR(sgr[:0], fg, bg))
				curFg, curBg = fg, bg
				havePrev = true
			}
			b.WriteRune(sh.Glyphs[mask])
		}
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// cell resolves one terminal cell to a foreground, a background, and the
// occupancy mask selecting its glyph. A uniform cell is reported as mask 0 with
// fg == bg, which renders as a space and lets long runs share one escape.
func (c *Canvas) cell(cx, cy int, buf []Color) (fg, bg Color, mask int) {
	sh := c.shape
	n := 0
	for ry := 0; ry < sh.Rows; ry++ {
		row := (cy*sh.Rows + ry) * c.fw
		for rx := 0; rx < sh.Cols; rx++ {
			buf[n] = c.pix[row+cx*sh.Cols+rx]
			n++
		}
	}

	// Fast path: at most two distinct colors, so the cell renders exactly.
	a := buf[0]
	var second Color
	haveSecond, crowded := false, false
	for i := 1; i < n; i++ {
		switch {
		case buf[i] == a:
		case !haveSecond:
			second, haveSecond = buf[i], true
		case buf[i] != second:
			crowded = true
		}
		if crowded {
			break
		}
	}
	if !crowded {
		if !haveSecond {
			return a, a, 0 // uniform
		}
		return c.orient(buf[:n], a, second)
	}

	// Crowded: keep the two most distant colors and snap the rest.
	bi, bj, best := 0, 1, -1.0
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if d := colorDist(buf[i], buf[j]); d > best {
				bi, bj, best = i, j, d
			}
		}
	}
	return c.orient(buf[:n], buf[bi], buf[bj])
}

// orient assigns two colors to foreground and background and builds the mask.
//
// The canvas background is kept as the cell's terminal background whenever it
// is one of the pair: block glyphs are drawn as foreground ink over that
// background, so any font whose glyphs do not tile a cell perfectly leaks the
// playfield color through the seams rather than the object's.
func (c *Canvas) orient(buf []Color, x, y Color) (fg, bg Color, mask int) {
	fg, bg = x, y
	if x == c.bg {
		fg, bg = y, x
	}
	for i, col := range buf {
		if colorDist(col, fg) <= colorDist(col, bg) {
			mask |= 1 << i
		}
	}
	return fg, bg, mask
}

// colorDist is squared RGB distance — monotonic in true distance, which is all
// the nearest-color comparisons need.
func colorDist(a, b Color) float64 {
	ar, ag, ab := a.RGB()
	br, bg, bb := b.RGB()
	dr := float64(ar) - float64(br)
	dg := float64(ag) - float64(bg)
	db := float64(ab) - float64(bb)
	return dr*dr + dg*dg + db*db
}

// appendSGR writes a truecolor foreground+background escape without the
// reflection overhead of fmt.
func appendSGR(dst []byte, fg, bg Color) []byte {
	fr, fgc, fb := fg.RGB()
	br, bgc, bb := bg.RGB()
	dst = append(dst, "\x1b[38;2;"...)
	dst = strconv.AppendUint(dst, uint64(fr), 10)
	dst = append(dst, ';')
	dst = strconv.AppendUint(dst, uint64(fgc), 10)
	dst = append(dst, ';')
	dst = strconv.AppendUint(dst, uint64(fb), 10)
	dst = append(dst, ";48;2;"...)
	dst = strconv.AppendUint(dst, uint64(br), 10)
	dst = append(dst, ';')
	dst = strconv.AppendUint(dst, uint64(bgc), 10)
	dst = append(dst, ';')
	dst = strconv.AppendUint(dst, uint64(bb), 10)
	dst = append(dst, 'm')
	return dst
}
