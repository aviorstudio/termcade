package sdk

import "math"

// Canvas is a fixed-size pixel buffer games draw into each frame.
//
// Games work in *logical* units — W by H, square on screen — and never see the
// pixel grid underneath. The canvas rasterizes into a finer buffer sized by the
// CellShape in use, so float geometry (Polyline, FillCircle, the ...F variants)
// picks up real detail instead of being upscaled. H must be even: a cell always
// spans two logical rows.
type Canvas struct {
	W, H   int // logical size, in square units
	shape  CellShape
	sx, sy float64 // pixels per logical unit, per axis
	fw, fh int     // pixel buffer size
	pix    []Color
	bg     Color
}

// NewCanvas builds a canvas of w by h logical units rendered with shape.
//
// The terminal footprint is w columns by h/2 rows for every shape; only the
// pixel density changes. Scales are chosen to keep logical units square: a
// logical unit is always one cell wide and half a cell tall, whatever the
// shape's own pixel aspect ratio happens to be.
func NewCanvas(w, h int, bg Color, shape CellShape) *Canvas {
	if h%2 != 0 {
		panic("engine: canvas logical height must be even")
	}
	c := &Canvas{
		W:     w,
		H:     h,
		shape: shape,
		sx:    float64(shape.Cols),
		sy:    float64(shape.Rows) / 2,
		fw:    w * shape.Cols,
		fh:    h * shape.Rows / 2,
		bg:    bg,
	}
	c.pix = make([]Color, c.fw*c.fh)
	c.Clear()
	return c
}

// Shape reports the cell subdivision this canvas renders with.
func (c *Canvas) Shape() CellShape { return c.shape }

// Pix exposes the raw pixel buffer, row-major, one Color per pixel. Advanced:
// it exists so a host process can copy frames across an embedding boundary
// (the wasm ABI); games should draw through the shape-aware methods instead.
// The slice is allocated once and never reallocated for the canvas lifetime.
func (c *Canvas) Pix() []Color { return c.pix }

// PixelSize reports the underlying pixel buffer dimensions.
func (c *Canvas) PixelSize() (w, h int) { return c.fw, c.fh }

func (c *Canvas) Clear() {
	for i := range c.pix {
		c.pix[i] = c.bg
	}
}

// SetPixel writes one buffer pixel; out-of-bounds coordinates are clipped.
func (c *Canvas) SetPixel(fx, fy int, col Color) {
	if fx < 0 || fx >= c.fw || fy < 0 || fy >= c.fh {
		return
	}
	c.pix[fy*c.fw+fx] = col
}

// AtPixel reads one buffer pixel, returning the background when out of bounds.
func (c *Canvas) AtPixel(fx, fy int) Color {
	if fx < 0 || fx >= c.fw || fy < 0 || fy >= c.fh {
		return c.bg
	}
	return c.pix[fy*c.fw+fx]
}

// Set fills the whole logical unit at (x, y) — a block of pixels whose size
// depends on the shape — so integer drawing looks the same at any density.
func (c *Canvas) Set(x, y int, col Color) {
	c.fillPixels(
		floorScale(float64(x), c.sx), floorScale(float64(x+1), c.sx),
		floorScale(float64(y), c.sy), floorScale(float64(y+1), c.sy),
		col)
}

// At samples the logical unit at (x, y) via its top-left pixel.
func (c *Canvas) At(x, y int) Color {
	if x < 0 || x >= c.W || y < 0 || y >= c.H {
		return c.bg
	}
	return c.AtPixel(floorScale(float64(x), c.sx), floorScale(float64(y), c.sy))
}

// SetF lights the single pixel containing the logical point (x, y), giving
// sub-unit placement for small objects like bullets.
func (c *Canvas) SetF(x, y float64, col Color) {
	c.SetPixel(floorScale(x, c.sx), floorScale(y, c.sy), col)
}

func (c *Canvas) FillRect(x, y, w, h int, col Color) {
	c.FillRectF(float64(x), float64(y), float64(w), float64(h), col)
}

// FillRectF fills a rectangle at logical float precision, so edges land on
// pixel boundaries rather than snapping to whole units.
func (c *Canvas) FillRectF(x, y, w, h float64, col Color) {
	c.fillPixels(
		floorScale(x, c.sx), floorScale(x+w, c.sx),
		floorScale(y, c.sy), floorScale(y+h, c.sy),
		col)
}

func (c *Canvas) fillPixels(fx0, fx1, fy0, fy1 int, col Color) {
	if fx0 < 0 {
		fx0 = 0
	}
	if fy0 < 0 {
		fy0 = 0
	}
	if fx1 > c.fw {
		fx1 = c.fw
	}
	if fy1 > c.fh {
		fy1 = c.fh
	}
	for fy := fy0; fy < fy1; fy++ {
		row := fy * c.fw
		for fx := fx0; fx < fx1; fx++ {
			c.pix[row+fx] = col
		}
	}
}

// Line draws a one-pixel line between two logical unit centers.
func (c *Canvas) Line(x0, y0, x1, y1 int, col Color) {
	c.LineF(float64(x0), float64(y0), float64(x1), float64(y1), col)
}

// LineF rasterizes a Bresenham line at pixel resolution. Endpoints are
// canonicalized so A->B and B->A light the same pixels.
func (c *Canvas) LineF(x0, y0, x1, y1 float64, col Color) {
	px0, py0 := c.toPixel(x0, y0)
	px1, py1 := c.toPixel(x1, y1)
	if px0 > px1 || (px0 == px1 && py0 > py1) {
		px0, px1 = px1, px0
		py0, py1 = py1, py0
	}
	dx := abs(px1 - px0)
	dy := -abs(py1 - py0)
	sx, sy := 1, 1
	if px0 > px1 {
		sx = -1
	}
	if py0 > py1 {
		sy = -1
	}
	err := dx + dy
	for {
		c.SetPixel(px0, py0, col)
		if px0 == px1 && py0 == py1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			px0 += sx
		}
		if e2 <= dx {
			err += dx
			py0 += sy
		}
	}
}

// FillCircle fills a disc of logical radius r. It rasterizes as an ellipse in
// pixel space because the two axes are scaled differently, which is exactly
// what makes it look round on screen.
func (c *Canvas) FillCircle(cx, cy, r float64, col Color) {
	rx, ry := r*c.sx, r*c.sy
	fcx, fcy := cx*c.sx, cy*c.sy
	x0 := int(math.Floor(fcx - rx))
	x1 := int(math.Ceil(fcx + rx))
	y0 := int(math.Floor(fcy - ry))
	y1 := int(math.Ceil(fcy + ry))
	for fy := y0; fy <= y1; fy++ {
		dy := (float64(fy) + 0.5 - fcy) / ry
		for fx := x0; fx <= x1; fx++ {
			dx := (float64(fx) + 0.5 - fcx) / rx
			if dx*dx+dy*dy <= 1 {
				c.SetPixel(fx, fy, col)
			}
		}
	}
}

// Polyline chains LineF segments through float points.
func (c *Canvas) Polyline(pts []Vec2, closed bool, col Color) {
	if len(pts) < 2 {
		return
	}
	for i := 0; i < len(pts)-1; i++ {
		c.LineF(pts[i].X, pts[i].Y, pts[i+1].X, pts[i+1].Y, col)
	}
	if closed {
		last := len(pts) - 1
		c.LineF(pts[last].X, pts[last].Y, pts[0].X, pts[0].Y, col)
	}
}

// toPixel maps a logical point to the center of its pixel, so short segments
// between neighbouring units still produce a connected run.
func (c *Canvas) toPixel(x, y float64) (int, int) {
	return int(math.Floor(x*c.sx + 0.5*c.sx)), int(math.Floor(y*c.sy + 0.5*c.sy))
}

// floorScale converts a logical coordinate to a pixel index. Flooring (rather
// than rounding) makes adjacent spans tile exactly, with no gaps or overlap,
// even when the scale is fractional.
func floorScale(v, scale float64) int { return int(math.Floor(v * scale)) }

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
