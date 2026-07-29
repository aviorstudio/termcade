package brickough

import (
	"math"

	"github.com/aviorstudio/termcade/sdk"
)

// AABB is an axis-aligned box in pixel space.
type AABB struct {
	X, Y, W, H float64
}

func (a AABB) overlaps(b AABB) bool {
	return a.X < b.X+b.W && a.X+a.W > b.X && a.Y < b.Y+b.H && a.Y+a.H > b.Y
}

// Axis identifies which velocity component a collision reflects.
type Axis int

const (
	AxisNone Axis = iota
	AxisX
	AxisY
)

// paddleBounce returns the ball velocity after a paddle hit. off is the hit
// position relative to the paddle center, in [-1, 1]; the rebound angle is
// off * 60° from vertical, and speed is preserved.
func paddleBounce(speed, off float64) sdk.Vec2 {
	if off < -1 {
		off = -1
	} else if off > 1 {
		off = 1
	}
	angle := off * (60 * math.Pi / 180)
	return sdk.Vec2{X: speed * math.Sin(angle), Y: -speed * math.Cos(angle)}
}

// brickHit reports whether the ball box hits the brick box and which axis to
// reflect: the axis of least penetration.
func brickHit(ball, brick AABB) (bool, Axis) {
	if !ball.overlaps(brick) {
		return false, AxisNone
	}
	overlapX := math.Min(ball.X+ball.W, brick.X+brick.W) - math.Max(ball.X, brick.X)
	overlapY := math.Min(ball.Y+ball.H, brick.Y+brick.H) - math.Max(ball.Y, brick.Y)
	if overlapX < overlapY {
		return true, AxisX
	}
	return true, AxisY
}
