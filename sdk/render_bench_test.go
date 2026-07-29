package sdk

import "testing"

// benchCanvas paints a playfield-sized canvas the way a busy frame looks:
// mostly background with vector art over it.
func benchCanvas(sh CellShape, busy bool) *Canvas {
	c := NewCanvas(72, 40, Black, sh)
	if busy {
		// Worst case for run-length batching: a color change every cell.
		cols := []Color{Red, Cyan, Yellow, Green, White}
		for y := 0; y < c.H; y++ {
			for x := 0; x < c.W; x++ {
				c.Set(x, y, cols[(x*7+y*3)%len(cols)])
			}
		}
		return c
	}
	for i := 0; i < 6; i++ {
		cx := float64(8 + i*10)
		c.Polyline([]Vec2{
			{X: cx, Y: 6}, {X: cx + 5, Y: 10}, {X: cx + 3, Y: 16}, {X: cx - 3, Y: 14},
		}, true, Gray)
	}
	c.FillCircle(36, 30, 2, Yellow)
	return c
}

func BenchmarkRender(b *testing.B) {
	for _, sh := range []CellShape{HalfBlock, Quadrant, Sextant} {
		for _, busy := range []bool{false, true} {
			name := sh.Name
			if busy {
				name += "/busy"
			} else {
				name += "/typical"
			}
			b.Run(name, func(b *testing.B) {
				c := benchCanvas(sh, busy)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					_ = c.Render()
				}
			})
		}
	}
}
