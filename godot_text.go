package main

import (
	"fmt"
	"math"
	"strings"

	"github.com/aviorstudio/termcade/internal/godot"
	"github.com/aviorstudio/termcade/sdk"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
)

type godotTextCell struct {
	value string
	color [3]uint8
	span  int
}

// Text replaces whole terminal cells, keeping the sampled game background.
// Cursor movement never enters Bubble Tea's view; all output has fixed width.
func renderGodotText(canvas *sdk.Canvas, runs []godot.TextRun) string {
	base := canvas.Render()
	if len(runs) == 0 {
		return base
	}
	lines := strings.Split(base, "\n")
	shape := canvas.Shape()
	width, height := canvas.PixelSize()
	cols, rows := width/shape.Cols, height/shape.Rows
	cells := make(map[int]godotTextCell)
	for _, run := range runs {
		left := int(math.Round(run.X / float64(shape.Cols)))
		top := int(math.Round(run.Y / float64(shape.Rows)))
		right := int(math.Round((run.X + run.W) / float64(shape.Cols)))
		bottom := max(top+1, int(math.Round((run.Y+run.H)/float64(shape.Rows))))
		if run.Block {
			for y := max(0, top); y < min(rows, bottom); y++ {
				for x := max(0, left); x < min(cols, right); x++ {
					eraseGodotTextCell(cells, y*cols+x, cols)
				}
			}
			continue
		}
		available := min(cols, right) - max(0, left)
		if available <= 0 || bottom <= top {
			continue
		}
		// Wrap at terminal character boundaries, independent of Godot font metrics.
		var wrapped []string
		for _, line := range strings.Split(run.Value, "\n") {
			line = safeGodotText(line)
			for ansi.StringWidth(line) > available {
				part := ansi.Truncate(line, available, "")
				if part == "" {
					break
				}
				wrapped = append(wrapped, part)
				line = line[len(part):]
			}
			wrapped = append(wrapped, line)
		}
		if len(wrapped) > bottom-top {
			wrapped = wrapped[:bottom-top]
		}
		y := top
		if run.Vertical == 1 {
			y += (bottom - top - len(wrapped)) / 2
		} else if run.Vertical == 2 {
			y += bottom - top - len(wrapped)
		}
		for _, line := range wrapped {
			x := left
			if run.Align == 1 {
				x += (right - left - ansi.StringWidth(line)) / 2
			} else if run.Align == 2 {
				x += right - left - ansi.StringWidth(line)
			}
			graphemes := uniseg.NewGraphemes(line)
			for graphemes.Next() {
				value := graphemes.Str()
				span := ansi.StringWidth(value)
				if span == 0 {
					continue
				}
				inside := x >= 0 && x+span <= cols && y >= 0 && y < rows && float64(x*shape.Cols) >= run.Clip[0]-0.5 && float64((x+span)*shape.Cols) <= run.Clip[2]+0.5 && float64(y*shape.Rows) >= run.Clip[1]-0.5 && float64((y+1)*shape.Rows) <= run.Clip[3]+0.5
				if inside {
					for i := range span {
						eraseGodotTextCell(cells, y*cols+x+i, cols)
					}
					cells[y*cols+x] = godotTextCell{value, run.Color, span}
					for i := 1; i < span; i++ {
						cells[y*cols+x+i] = godotTextCell{span: -1}
					}
				}
				x += span
			}
			y++
		}
	}
	var output strings.Builder
	pixels := canvas.Pix()
	for y, line := range lines {
		if y > 0 {
			output.WriteByte('\n')
		}
		for x := 0; x < cols; {
			cell, ok := cells[y*cols+x]
			if !ok || cell.span < 1 {
				end := x + 1
				for end < cols {
					if next, found := cells[y*cols+end]; found && next.span > 0 {
						break
					}
					end++
				}
				output.WriteString(ansi.Cut(line, x, end))
				x = end
				continue
			}
			bg := pixels[(y*shape.Rows+shape.Rows/2)*width+x*shape.Cols+shape.Cols/2]
			r, g, b := bg.RGB()
			fmt.Fprintf(&output, "\x1b[38;2;%d;%d;%d;48;2;%d;%d;%dm%s\x1b[0m", cell.color[0], cell.color[1], cell.color[2], r, g, b, cell.value)
			x += cell.span
		}
		output.WriteString("\x1b[0m")
	}
	return output.String()
}

func eraseGodotTextCell(cells map[int]godotTextCell, index, columns int) {
	start := index
	for start > index-index%columns && cells[start].span < 0 {
		start--
	}
	span := cells[start].span
	if span > 0 {
		for i := range span {
			delete(cells, start+i)
		}
	} else {
		delete(cells, index)
	}
}
