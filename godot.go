package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/aviorstudio/termcade/internal/godot"
	"github.com/aviorstudio/termcade/sdk"
)

const godotUsage = "usage: termcade godot init <project> | export [--json] <project> <game.tgd> | play --trusted <game.tgd> | capture --trusted [--frames N] [--input replay.json] <game.tgd> <frame.png>"

func cmdGodot(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", godotUsage)
	}
	options := flag.NewFlagSet("godot "+args[0], flag.ContinueOnError)
	options.SetOutput(io.Discard)
	trusted := options.Bool("trusted", false, "execute a local project as native code")
	machine := options.Bool("json", false, "emit structured results")
	frames := options.Int("frames", 1, "number of simulation frames to capture")
	input := options.String("input", "", "JSON replay: array of {frame,code,down}")
	if err := options.Parse(args[1:]); err != nil {
		return err
	}
	positional := options.Args()
	if args[0] != "init" && args[0] != "export" && args[0] != "play" && args[0] != "capture" {
		return fmt.Errorf("%s", godotUsage)
	}
	if (args[0] == "play" || args[0] == "capture") && !*trusted {
		return fmt.Errorf("Godot .tgd projects execute native code; use --trusted for your local project (marketplace .tcade games retain their Wasm sandbox)")
	}
	ctx := context.Background()
	bin, err := godot.Binary(ctx)
	if err != nil {
		return err
	}
	switch args[0] {
	case "init":
		if len(positional) != 1 {
			return fmt.Errorf("%s", godotUsage)
		}
		if err := godot.InstallAddon(ctx, bin, positional[0]); err != nil {
			return err
		}
		return godotResult(*machine, "init", positional[0])
	case "export":
		if len(positional) != 2 {
			return fmt.Errorf("%s", godotUsage)
		}
		if err := godot.Export(ctx, bin, positional[0], positional[1]); err != nil {
			return err
		}
		return godotResult(*machine, "export", positional[1])
	case "play", "capture":
		want := 1
		if args[0] == "capture" {
			want = 2
		}
		if len(positional) != want || *frames < 1 || *frames > 3600 {
			return fmt.Errorf("%s", godotUsage)
		}
		runtime, err := godot.Start(ctx, bin, positional[0])
		if err != nil {
			return err
		}
		defer runtime.Close()
		canvas := sdk.NewCanvas(72, 40, sdk.Black, pixelShape())
		width, height := canvas.PixelSize()
		pixels, err := runtime.Reset(width, height)
		if err != nil {
			return err
		}
		copy(canvas.Pix(), pixels)
		if args[0] == "capture" {
			if err := captureGodot(runtime, canvas, *frames, *input, positional[1]); err != nil {
				return err
			}
			return godotResult(*machine, "capture", positional[1])
		}
		model := godotPlayer{runtime: runtime, canvas: canvas, held: map[int]int{}}
		_, err = tea.NewProgram(model).Run()
		return err
	default:
		return fmt.Errorf("%s", godotUsage)
	}
}

func godotResult(machine bool, operation, artifact string) error {
	if machine {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "operation": operation, "artifact": artifact, "runtime": "godot-terminal-2d-v1", "godot": godot.EngineVersion})
	}
	fmt.Println("Godot terminal", operation+":", artifact)
	return nil
}

type replayEvent struct {
	Frame int `json:"frame"`
	godot.KeyEvent
}

func captureGodot(runtime *godot.Runtime, canvas *sdk.Canvas, frames int, input, destination string) error {
	var events []replayEvent
	if input != "" {
		file, err := os.Open(input)
		if err != nil {
			return err
		}
		decoder := json.NewDecoder(io.LimitReader(file, 65537))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&events)
		if err == nil {
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				err = fmt.Errorf("replay must contain exactly one JSON array")
			}
		}
		file.Close()
		if err != nil || len(events) > 256 {
			return fmt.Errorf("invalid bounded input replay: %v", err)
		}
		for _, event := range events {
			if event.Frame < 1 || event.Frame > frames || event.Code < 1 || event.Code > 1<<24 {
				return fmt.Errorf("input replay event is out of range")
			}
		}
	}
	for frame := 1; frame <= frames; frame++ {
		var keys []godot.KeyEvent
		for _, event := range events {
			if event.Frame == frame {
				keys = append(keys, event.KeyEvent)
			}
		}
		pixels, err := runtime.Step(keys)
		if err != nil {
			return err
		}
		copy(canvas.Pix(), pixels)
	}
	width, height := canvas.PixelSize()
	frame := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			r, g, b := canvas.AtPixel(x, y).RGB()
			frame.SetRGBA(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if err = png.Encode(file, frame); err != nil {
		file.Close()
		os.Remove(destination)
		return err
	}
	return file.Close()
}

type godotTick struct{}
type godotFrame struct {
	pixels []sdk.Color
	err    error
}

// The native engine exchange runs as a Tea command, keeping resize, pause and
// exit responsive even while a game is hung. Only one exchange is in flight.
type godotPlayer struct {
	runtime *godot.Runtime
	canvas  *sdk.Canvas
	held    map[int]int
	keys    []godot.KeyEvent
	ticks   int
	exact   bool
	busy    bool
	paused  bool
	err     error
	width   int
	height  int
}

func (m godotPlayer) Init() tea.Cmd { return godotNextTick() }

func godotNextTick() tea.Cmd {
	return tea.Tick(time.Second/60, func(time.Time) tea.Msg { return godotTick{} })
}

func (m godotPlayer) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit
		case "ctrl+p":
			m.paused = !m.paused
			if m.paused {
				for code := range m.held {
					m.keys = append(m.keys, godot.KeyEvent{Code: code})
				}
				clear(m.held)
			}
			return m, nil
		}
		if code := godotKey(msg.String()); code != 0 && !m.paused && len(m.keys) < 256 {
			m.held[code] = m.ticks
			m.keys = append(m.keys, godot.KeyEvent{Code: code, Down: true})
		}
	case tea.KeyReleaseMsg:
		m.exact = true
		if code := godotKey(msg.String()); code != 0 && len(m.keys) < 256 {
			delete(m.held, code)
			m.keys = append(m.keys, godot.KeyEvent{Code: code})
		}
	case godotTick:
		if !m.paused && m.err == nil && !m.busy && m.width >= 72 && m.height >= 22 {
			m.ticks++
			if !m.exact {
				for code, last := range m.held {
					if m.ticks-last >= sdk.DefaultHoldTicks {
						m.keys = append(m.keys, godot.KeyEvent{Code: code})
						delete(m.held, code)
					}
				}
			}
			keys := m.keys
			m.keys = nil
			m.busy = true
			return m, func() tea.Msg {
				pixels, err := m.runtime.Step(keys)
				return godotFrame{pixels: pixels, err: err}
			}
		}
		return m, godotNextTick()
	case godotFrame:
		m.busy = false
		m.err = msg.err
		if msg.err == nil {
			copy(m.canvas.Pix(), msg.pixels)
		}
		return m, godotNextTick()
	}
	return m, nil
}

func (m godotPlayer) View() tea.View {
	content := ""
	if m.width < 72 || m.height < 22 {
		content = "Godot terminal preview needs at least 72 columns × 22 rows."
	} else {
		content = m.canvas.Render() + "\n" + safeGodotText(m.runtime.Title) + " · Ctrl+P pause · Esc exit"
		if m.paused {
			content += " · PAUSED"
		}
		if m.err != nil {
			content += "\n" + safeGodotText(m.err.Error())
		}
	}
	view := tea.NewView(content)
	view.AltScreen = true
	view.KeyboardEnhancements.ReportEventTypes = true
	return view
}

func safeGodotText(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
}

func godotKey(key string) int {
	const special = 1 << 22
	switch key {
	case "left":
		return special + 15
	case "right":
		return special + 17
	case "up":
		return special + 16
	case "down":
		return special + 18
	case "enter":
		return special + 5
	case "tab":
		return special + 2
	case "backspace":
		return special + 4
	case "space", " ":
		return 32
	}
	runes := []rune(key)
	if len(runes) == 1 {
		return int(unicode.ToUpper(runes[0]))
	}
	return 0
}
