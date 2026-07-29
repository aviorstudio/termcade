package sdk

import (
	"math"
	"testing"
)

const eps = 1e-9

func vecEq(a, b Vec2) bool {
	return math.Abs(a.X-b.X) < eps && math.Abs(a.Y-b.Y) < eps
}

func TestVecOps(t *testing.T) {
	if got := (Vec2{1, 2}).Add(Vec2{3, -1}); !vecEq(got, Vec2{4, 1}) {
		t.Errorf("Add = %v", got)
	}
	if got := (Vec2{1, 2}).Sub(Vec2{3, -1}); !vecEq(got, Vec2{-2, 3}) {
		t.Errorf("Sub = %v", got)
	}
	if got := (Vec2{1, -2}).Scale(2.5); !vecEq(got, Vec2{2.5, -5}) {
		t.Errorf("Scale = %v", got)
	}
	if got := (Vec2{3, 4}).Len(); math.Abs(got-5) > eps {
		t.Errorf("Len = %v", got)
	}
}

func TestVecRotate(t *testing.T) {
	cases := []struct {
		in    Vec2
		theta float64
		want  Vec2
	}{
		{Vec2{1, 0}, math.Pi / 2, Vec2{0, 1}},
		{Vec2{1, 0}, math.Pi, Vec2{-1, 0}},
		{Vec2{0, 1}, -math.Pi / 2, Vec2{1, 0}},
		{Vec2{2, 3}, 0, Vec2{2, 3}},
	}
	for _, c := range cases {
		if got := c.in.Rotate(c.theta); !vecEq(got, c.want) {
			t.Errorf("%v.Rotate(%v) = %v, want %v", c.in, c.theta, got, c.want)
		}
	}
	// Rotation preserves length.
	v := Vec2{3.7, -1.2}
	if got := v.Rotate(1.234).Len(); math.Abs(got-v.Len()) > eps {
		t.Errorf("Rotate changed length: %v vs %v", got, v.Len())
	}
}
