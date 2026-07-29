package asteroid

import (
	"math"

	"github.com/aviorstudio/termcade/sdk"
)

// wrap maps a position onto the toroidal playfield.
func wrap(p sdk.Vec2, w, h float64) sdk.Vec2 {
	x := math.Mod(p.X, w)
	if x < 0 {
		x += w
	}
	y := math.Mod(p.Y, h)
	if y < 0 {
		y += h
	}
	return sdk.Vec2{X: x, Y: y}
}

// toroidalDist returns the shortest distance between two points on a
// wrapping field, taking the seam into account per axis.
func toroidalDist(a, b sdk.Vec2, w, h float64) float64 {
	dx := math.Abs(a.X - b.X)
	if dx > w/2 {
		dx = w - dx
	}
	dy := math.Abs(a.Y - b.Y)
	if dy > h/2 {
		dy = h - dy
	}
	return math.Hypot(dx, dy)
}

// hit reports whether two points are within r of each other on the torus.
func hit(a, b sdk.Vec2, r, w, h float64) bool {
	return toroidalDist(a, b, w, h) < r
}
