package sdk

import (
	"fmt"
	"math"
	"testing"
)

func newTestCanvas(w, h int) *Canvas { return NewCanvas(w, h, Black, Quadrant) }

// pixels reports which logical units have any lit pixel. A logical unit covers
// several buffer pixels once the shape subdivides a cell, so "lit" means any of
// them, not a single sample point.
func pixels(c *Canvas) map[[2]int]Color {
	set := map[[2]int]Color{}
	fw, fh := c.PixelSize()
	for fy := 0; fy < fh; fy++ {
		for fx := 0; fx < fw; fx++ {
			col := c.AtPixel(fx, fy)
			if col == c.bg {
				continue
			}
			lx := int(math.Floor(float64(fx) / c.sx))
			ly := int(math.Floor(float64(fy) / c.sy))
			set[[2]int{lx, ly}] = col
		}
	}
	return set
}

func TestSetClipsWithoutPanic(t *testing.T) {
	c := newTestCanvas(4, 4)
	for _, p := range [][2]int{{-1, 0}, {0, -1}, {4, 0}, {0, 4}, {-100, -100}, {100, 100}} {
		c.Set(p[0], p[1], White) // must not panic
	}
	if len(pixels(c)) != 0 {
		t.Errorf("out-of-bounds Set leaked pixels: %v", pixels(c))
	}
	if got := c.At(-1, 99); got != Black {
		t.Errorf("out-of-bounds At = %v, want bg", got)
	}
}

func TestOddHeightPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewCanvas with odd height did not panic")
		}
	}()
	newTestCanvas(4, 5)
}

// TestShapesShareFootprint pins the property the whole design rests on: the
// playfield occupies the same terminal cells at every density, so switching
// shapes changes sharpness and nothing else.
func TestShapesShareFootprint(t *testing.T) {
	for _, sh := range []CellShape{HalfBlock, Quadrant, Sextant} {
		t.Run(sh.Name, func(t *testing.T) {
			c := NewCanvas(64, 40, Black, sh)
			fw, fh := c.PixelSize()
			if cols := fw / sh.Cols; cols != 64 {
				t.Errorf("%s: %d cell columns, want 64", sh.Name, cols)
			}
			if rows := fh / sh.Rows; rows != 20 {
				t.Errorf("%s: %d cell rows, want 20", sh.Name, rows)
			}
			// Logical units stay square: one unit is one cell wide and half a
			// cell tall regardless of the subdivision.
			if c.sx != float64(sh.Cols) || c.sy != float64(sh.Rows)/2 {
				t.Errorf("%s: scales %v,%v do not keep units square", sh.Name, c.sx, c.sy)
			}
		})
	}
}

func TestFillRectExtents(t *testing.T) {
	c := newTestCanvas(8, 8)
	c.FillRect(2, 3, 3, 2, Red)
	got := pixels(c)
	if len(got) != 6 {
		t.Fatalf("FillRect set %d pixels, want 6: %v", len(got), got)
	}
	for y := 3; y < 5; y++ {
		for x := 2; x < 5; x++ {
			if got[[2]int{x, y}] != Red {
				t.Errorf("pixel (%d,%d) not set", x, y)
			}
		}
	}
	// Partially off-canvas rect clips cleanly.
	c2 := newTestCanvas(4, 4)
	c2.FillRect(-2, -2, 4, 4, Green)
	if len(pixels(c2)) != 4 {
		t.Errorf("clipped FillRect set %d pixels, want 4", len(pixels(c2)))
	}
}

// TestFillRectFSubUnit checks that float rects actually land between logical
// units — the reason the paddle stops snapping.
func TestFillRectFSubUnit(t *testing.T) {
	a := NewCanvas(8, 8, Black, Quadrant)
	a.FillRectF(2, 0, 2, 1, White)
	b := NewCanvas(8, 8, Black, Quadrant)
	b.FillRectF(2.5, 0, 2, 1, White)
	if a.Render() == b.Render() {
		t.Error("half-unit offset produced an identical frame; sub-unit detail lost")
	}
}

func TestLineEndpointsAndSymmetry(t *testing.T) {
	cases := [][4]int{
		{0, 0, 7, 0}, // horizontal
		{0, 0, 0, 7}, // vertical
		{0, 0, 7, 7}, // diagonal
		{0, 0, 7, 3}, // shallow
		{0, 0, 3, 7}, // steep
		{7, 5, 1, 0}, // negative direction
		{5, 5, 5, 5}, // single point
		{0, 7, 7, 0}, // anti-diagonal
	}
	for _, s := range cases {
		t.Run(fmt.Sprintf("%v", s), func(t *testing.T) {
			a := newTestCanvas(8, 8)
			a.Line(s[0], s[1], s[2], s[3], White)
			pa := pixels(a)
			if pa[[2]int{s[0], s[1]}] != White || pa[[2]int{s[2], s[3]}] != White {
				t.Errorf("endpoints missing: %v", pa)
			}
			// Symmetry: drawing B->A covers the same pixels.
			b := newTestCanvas(8, 8)
			b.Line(s[2], s[3], s[0], s[1], White)
			pb := pixels(b)
			if len(pa) != len(pb) {
				t.Fatalf("A->B has %d pixels, B->A has %d", len(pa), len(pb))
			}
			for p := range pa {
				if pb[p] != White {
					t.Errorf("pixel %v in A->B missing from B->A", p)
				}
			}
		})
	}
}

func TestFillCircle(t *testing.T) {
	c := newTestCanvas(16, 16)
	c.FillCircle(8, 8, 3, Cyan)
	got := pixels(c)
	if got[[2]int{8, 8}] != Cyan {
		t.Error("center not filled")
	}
	if got[[2]int{8, 5}] != Cyan || got[[2]int{5, 8}] != Cyan {
		t.Error("cardinal extremes not filled")
	}
	if _, ok := got[[2]int{4, 4}]; ok {
		t.Error("corner outside radius was filled")
	}
}

// TestFillCircleStaysRound guards the anisotropic scaling: a disc must cover
// the same span of logical units horizontally and vertically even though the
// two axes have different pixel densities.
func TestFillCircleStaysRound(t *testing.T) {
	for _, sh := range []CellShape{HalfBlock, Quadrant, Sextant} {
		t.Run(sh.Name, func(t *testing.T) {
			c := NewCanvas(32, 32, Black, sh)
			c.FillCircle(16, 16, 6, Cyan)
			got := pixels(c)
			var minX, maxX, minY, maxY int = 1 << 30, -1, 1 << 30, -1
			for p := range got {
				minX, maxX = min(minX, p[0]), max(maxX, p[0])
				minY, maxY = min(minY, p[1]), max(maxY, p[1])
			}
			w, h := maxX-minX+1, maxY-minY+1
			if d := w - h; d < -1 || d > 1 {
				t.Errorf("%s: disc spans %dx%d logical units; not round", sh.Name, w, h)
			}
		})
	}
}

func TestPolyline(t *testing.T) {
	c := newTestCanvas(8, 8)
	tri := []Vec2{{1, 1}, {6, 1}, {3.6, 5.4}}
	c.Polyline(tri, true, Yellow)
	got := pixels(c)
	for _, p := range [][2]int{{1, 1}, {6, 1}, {4, 5}} {
		if got[p] != Yellow {
			t.Errorf("vertex %v not drawn", p)
		}
	}
	// Open polyline with 1 point draws nothing and doesn't panic.
	c2 := newTestCanvas(4, 4)
	c2.Polyline([]Vec2{{1, 1}}, false, Yellow)
	if len(pixels(c2)) != 0 {
		t.Error("single-point polyline drew pixels")
	}
}
