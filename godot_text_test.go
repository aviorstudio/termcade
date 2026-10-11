package main

import (
	"strings"
	"testing"

	"github.com/aviorstudio/termcade/internal/godot"
	"github.com/aviorstudio/termcade/sdk"
	"github.com/charmbracelet/x/ansi"
)

func TestGodotTextOverlay(t *testing.T) {
	canvas := sdk.NewCanvas(20, 8, sdk.Color(0x123456), sdk.Quadrant)
	run := godot.TextRun{X: 4, Y: 2, W: 20, H: 4, Value: "Hello 世界", Align: 1, Color: [3]uint8{255, 240, 220}, Clip: [4]float64{0, 0, 40, 8}}
	rendered := renderGodotText(canvas, []godot.TextRun{run})
	plain := ansi.Strip(rendered)
	if !strings.Contains(plain, "Hello 世界") {
		t.Fatalf("text was not preserved: %q", plain)
	}
	for _, line := range strings.Split(plain, "\n") {
		if ansi.StringWidth(line) != 20 {
			t.Fatalf("terminal row width changed: %q", line)
		}
	}
	if !strings.Contains(rendered, "48;2;18;52;86m") {
		t.Fatal("game background was lost")
	}
	// A later panel covers earlier text, but a caption within it remains visible.
	panel := godot.TextRun{X: 0, Y: 0, W: 40, H: 8, Block: true}
	caption := run
	caption.Value = "Top"
	plain = ansi.Strip(renderGodotText(canvas, []godot.TextRun{run, panel, caption}))
	if strings.Contains(plain, "Hello") || !strings.Contains(plain, "Top") {
		t.Fatalf("panel ordering failed: %q", plain)
	}
	run.Value = "\x1b[2Junsafe\a"
	run.Clip = [4]float64{0, 0, 2, 2}
	rendered = renderGodotText(canvas, []godot.TextRun{run})
	if strings.Contains(rendered, "\x1b[2J") || strings.Contains(rendered, "unsafe") {
		t.Fatal("clipped or terminal control text escaped")
	}
}
