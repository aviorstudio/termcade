package brickough

import (
	"math"
	"testing"

	"github.com/aviorstudio/termcade/sdk"
)

func TestPaddleBounceCenter(t *testing.T) {
	v := paddleBounce(30, 0)
	if math.Abs(v.X) > 1e-9 {
		t.Errorf("center hit has horizontal component: %v", v)
	}
	if v.Y >= 0 {
		t.Errorf("center hit not moving up: %v", v)
	}
	if math.Abs(v.Len()-30) > 1e-9 {
		t.Errorf("speed not preserved: %v", v.Len())
	}
}

func TestPaddleBounceEdges(t *testing.T) {
	right := paddleBounce(30, 1)
	wantX := 30 * math.Sin(60*math.Pi/180)
	if math.Abs(right.X-wantX) > 1e-9 {
		t.Errorf("right edge X = %v, want %v", right.X, wantX)
	}
	if right.Y >= 0 {
		t.Errorf("right edge not moving up: %v", right)
	}

	left := paddleBounce(30, -1)
	if math.Abs(left.X+wantX) > 1e-9 {
		t.Errorf("left edge X = %v, want %v", left.X, -wantX)
	}

	// Out-of-range offsets clamp rather than exceeding 60°.
	over := paddleBounce(30, 5)
	if math.Abs(over.X-right.X) > 1e-9 || math.Abs(over.Y-right.Y) > 1e-9 {
		t.Errorf("offset not clamped: %v vs %v", over, right)
	}

	for _, off := range []float64{-1, -0.5, 0, 0.3, 1} {
		if v := paddleBounce(42, off); math.Abs(v.Len()-42) > 1e-9 {
			t.Errorf("speed not preserved at off=%v: %v", off, v.Len())
		}
	}
}

func TestBrickHitAxis(t *testing.T) {
	brick := AABB{X: 10, Y: 10, W: 8, H: 3}

	// Ball hitting from the left: shallow X penetration -> reflect X.
	fromLeft := AABB{X: 9.5, Y: 10.5, W: 2, H: 2}
	hit, axis := brickHit(fromLeft, brick)
	if !hit || axis != AxisX {
		t.Errorf("from left: hit=%v axis=%v", hit, axis)
	}

	// Ball hitting from above: shallow Y penetration -> reflect Y.
	fromAbove := AABB{X: 13, Y: 8.5, W: 2, H: 2}
	hit, axis = brickHit(fromAbove, brick)
	if !hit || axis != AxisY {
		t.Errorf("from above: hit=%v axis=%v", hit, axis)
	}

	// No overlap -> no hit.
	if hit, _ := brickHit(AABB{X: 0, Y: 0, W: 2, H: 2}, brick); hit {
		t.Error("non-overlapping boxes reported hit")
	}
	// Touching edges (zero overlap) -> no hit.
	if hit, _ := brickHit(AABB{X: 8, Y: 10, W: 2, H: 2}, brick); hit {
		t.Error("edge-touching boxes reported hit")
	}
}

func TestBuildLevel(t *testing.T) {
	l1 := buildLevel(1)
	if len(l1) != 4*brickCols {
		t.Errorf("level 1 has %d bricks, want %d", len(l1), 4*brickCols)
	}
	l3 := buildLevel(3)
	if len(l3) != 6*brickCols {
		t.Errorf("level 3 has %d bricks, want %d", len(l3), 6*brickCols)
	}
	// Rows cap at the palette size.
	l9 := buildLevel(9)
	if len(l9) != 6*brickCols {
		t.Errorf("level 9 has %d bricks, want %d", len(l9), 6*brickCols)
	}
	// All bricks inside the field.
	for _, b := range l3 {
		if b.Box.X < 0 || b.Box.X+b.Box.W > fieldW || b.Box.Y < 0 || b.Box.Y+b.Box.H > paddleY {
			t.Errorf("brick out of bounds: %+v", b.Box)
		}
		if !b.Alive || b.HP < 1 {
			t.Errorf("brick not initialized: %+v", b)
		}
	}
}

// TestNoTunnelingAtHighSpeed simulates many frames at level-9 ball speed and
// asserts the ball never ends a frame inside a live brick.
func TestNoTunnelingAtHighSpeed(t *testing.T) {
	g := &game{}
	g.Reset()
	g.level = 9
	g.startLevel()
	g.ballStuck = false
	g.vel = paddleBounce(g.ballSpeed(), 0.7)
	g.ball = sdk.Vec2{X: 32, Y: 30}

	for frame := 0; frame < sdk.TPS*60; frame++ { // one simulated minute
		if g.Update() == sdk.StatusGameOver {
			g.lives = 3
			g.resetBall()
			g.ballStuck = false
			g.vel = paddleBounce(g.ballSpeed(), -0.4)
			continue
		}
		half := ballSize / 2
		ballBox := AABB{X: g.ball.X - half, Y: g.ball.Y - half, W: ballSize, H: ballSize}
		for _, b := range g.bricks {
			if b.Alive && ballBox.overlaps(b.Box) {
				// Being in contact for one frame is fine mid-resolution, but
				// deep containment means tunneling.
				cx, cy := g.ball.X, g.ball.Y
				if cx > b.Box.X+1 && cx < b.Box.X+b.Box.W-1 && cy > b.Box.Y+1 && cy < b.Box.Y+b.Box.H-1 {
					t.Fatalf("frame %d: ball center (%v,%v) deep inside brick %+v", frame, cx, cy, b.Box)
				}
			}
		}
		if g.levelCleared() {
			return // cleared the level without tunneling; done
		}
	}
}
