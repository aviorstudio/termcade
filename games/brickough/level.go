package brickough

import "github.com/aviorstudio/termcade/sdk"

type Brick struct {
	Box   AABB
	HP    int
	Value int
	Color sdk.Color
	Alive bool
}

const (
	brickW    = 8.0
	brickH    = 3.0
	brickCols = 8
	brickTopY = 4.0
)

// rowSpec styles one row of bricks; earlier entries are higher rows.
type rowSpec struct {
	hp    int
	value int
	color sdk.Color
}

var rowPalette = []rowSpec{
	{2, 70, sdk.Red},
	{2, 60, sdk.Orange},
	{1, 50, sdk.Yellow},
	{1, 40, sdk.Green},
	{1, 30, sdk.Cyan},
	{1, 20, sdk.Blue},
}

// buildLevel lays out the brick grid for a level. Levels grow from 4 to 6
// rows, then stay at 6 while ball speed keeps rising.
func buildLevel(level int) []Brick {
	rows := 3 + level
	if rows > len(rowPalette) {
		rows = len(rowPalette)
	}
	specs := rowPalette[len(rowPalette)-rows:]
	bricks := make([]Brick, 0, rows*brickCols)
	for r, spec := range specs {
		for c := 0; c < brickCols; c++ {
			bricks = append(bricks, Brick{
				Box: AABB{
					X: float64(c) * brickW,
					Y: brickTopY + float64(r)*brickH,
					W: brickW,
					H: brickH,
				},
				HP:    spec.hp,
				Value: spec.value,
				Color: spec.color,
				Alive: true,
			})
		}
	}
	return bricks
}
