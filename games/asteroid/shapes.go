package asteroid

import (
	"math"
	"math/rand"

	"github.com/aviorstudio/termcade/sdk"
)

// shipModel is the ship polygon in model space, nose pointing along +X
// (angle 0). Index 0 is the nose — bullets spawn from it.
var shipModel = []sdk.Vec2{
	{X: 3.2, Y: 0},
	{X: -2.2, Y: 2.0},
	{X: -1.2, Y: 0},
	{X: -2.2, Y: -2.0},
}

// flameModel is the thrust flame drawn behind the ship while accelerating.
var flameModel = []sdk.Vec2{
	{X: -1.6, Y: 1.0},
	{X: -3.6, Y: 0},
	{X: -1.6, Y: -1.0},
}

// transform rotates a model polygon and translates it to pos.
func transform(model []sdk.Vec2, angle float64, pos sdk.Vec2) []sdk.Vec2 {
	out := make([]sdk.Vec2, len(model))
	for i, p := range model {
		out[i] = p.Rotate(angle).Add(pos)
	}
	return out
}

// rockShape generates a jittered polygon for a rock of radius r:
// 8-10 vertices at roughly even angles, radii in [0.75r, 1.15r].
func rockShape(rng *rand.Rand, r float64) []sdk.Vec2 {
	n := 8 + rng.Intn(3)
	pts := make([]sdk.Vec2, n)
	for i := range pts {
		theta := float64(i)*2*math.Pi/float64(n) + rng.Float64()*0.35
		radius := r * (0.75 + rng.Float64()*0.4)
		pts[i] = sdk.Vec2{X: radius * math.Cos(theta), Y: radius * math.Sin(theta)}
	}
	return pts
}
