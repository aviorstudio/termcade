package sdk

import "math"

// Vec2 is a 2D vector in playfield pixel space.
type Vec2 struct {
	X, Y float64
}

func (v Vec2) Add(o Vec2) Vec2      { return Vec2{v.X + o.X, v.Y + o.Y} }
func (v Vec2) Sub(o Vec2) Vec2      { return Vec2{v.X - o.X, v.Y - o.Y} }
func (v Vec2) Scale(s float64) Vec2 { return Vec2{v.X * s, v.Y * s} }
func (v Vec2) Len() float64         { return math.Hypot(v.X, v.Y) }

// Rotate returns v rotated by theta radians (counter-clockwise in math
// coordinates; on the canvas, where Y grows downward, this reads clockwise).
func (v Vec2) Rotate(theta float64) Vec2 {
	sin, cos := math.Sincos(theta)
	return Vec2{v.X*cos - v.Y*sin, v.X*sin + v.Y*cos}
}
