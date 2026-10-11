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
	"sort"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/aviorstudio/termcade/internal/godot"
	"github.com/aviorstudio/termcade/sdk"
)

const godotUsage = "usage: termcade godot init <project> | export [--json] <project> <game.tgd> | play --trusted <game.tgd> | benchmark --trusted [--frames N] [--warmup N] <game.tgd> | capture --trusted [--columns N] [--rows N] [--frames N] [--input replay.json] <game.tgd> <frame.png>"

func cmdGodot(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", godotUsage)
	}
	options := flag.NewFlagSet("godot "+args[0], flag.ContinueOnError)
	options.SetOutput(io.Discard)
	trusted := options.Bool("trusted", false, "execute a local project as native code")
	machine := options.Bool("json", false, "emit structured results")
	columns := options.Int("columns", 72, "capture width in terminal columns")
	rows := options.Int("rows", 20, "capture height in terminal rows")
	warmup := options.Int("warmup", 60, "benchmark warmup frames")
	frames := options.Int("frames", 1, "number of simulation frames to capture")
	input := options.String("input", "", "JSON replay: array of {frame,code,down}")
	if err := options.Parse(args[1:]); err != nil {
		return err
	}
	positional := options.Args()
	if args[0] != "init" && args[0] != "export" && args[0] != "play" && args[0] != "capture" && args[0] != "benchmark" {
		return fmt.Errorf("%s", godotUsage)
	}
	if (args[0] == "play" || args[0] == "capture" || args[0] == "benchmark") && !*trusted {
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
	case "play", "capture", "benchmark":
		want := 1
		if args[0] == "capture" {
			want = 2
		}
		if len(positional) != want || *frames < 1 || *frames > 3600 || (args[0] == "benchmark" && (*warmup < 0 || *warmup > 3600-*frames)) {
			return fmt.Errorf("%s", godotUsage)
		}
		runtime, err := godot.Start(ctx, bin, positional[0])
		if err != nil {
			return err
		}
		defer runtime.Close()
		shape := pixelShape()
		if *columns < 1 || *columns > 600/shape.Cols || *rows < 1 || *rows > 360/shape.Rows {
			return fmt.Errorf("capture dimensions exceed the framebuffer limit")
		}
		canvas := sdk.NewCanvas(*columns, *rows*2, sdk.Black, shape)
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
		if args[0] == "benchmark" {
			return benchmarkGodot(runtime, canvas, *frames, *warmup, *input, *machine)
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
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "operation": operation, "artifact": artifact, "runtime": "godot-terminal-framebuffer-v1", "godot": godot.EngineVersion})
	}
	fmt.Println("Godot terminal", operation+":", artifact)
	return nil
}

type replayEvent struct {
	Frame int `json:"frame"`
	godot.KeyEvent
}

func loadGodotReplay(input string, frames int) ([]replayEvent, error) {
	var events []replayEvent
	if input != "" {
		file, err := os.Open(input)
		if err != nil {
			return nil, err
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
			return nil, fmt.Errorf("invalid bounded input replay: %v", err)
		}
		for _, event := range events {
			if event.Frame < 1 || event.Frame > frames || event.Code < 1 || event.Code > 1<<24 {
				return nil, fmt.Errorf("input replay event is out of range")
			}
		}
	}
	return events, nil
}

func captureGodot(runtime *godot.Runtime, canvas *sdk.Canvas, frames int, input, destination string) error {
	events, err := loadGodotReplay(input, frames)
	if err != nil {
		return err
	}
	for frame := 1; frame <= frames; frame++ {
		var keys []godot.KeyEvent
		for _, event := range events {
			if event.Frame == frame {
				keys = append(keys, event.KeyEvent)
			}
		}
		width, height := canvas.PixelSize()
		shape := canvas.Shape()
		pixels, err := runtime.StepCanvas(keys, width, height, float64(shape.Rows)/float64(2*shape.Cols))
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
	started time.Time
	canvas  *sdk.Canvas
	pixels  []sdk.Color
	err     error
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

func (m godotPlayer) Init() tea.Cmd { return godotNextTick(time.Now()) }

func godotNextTick(started time.Time) tea.Cmd {
	return tea.Tick(godotFrameDelay(started, time.Now()), func(time.Time) tea.Msg { return godotTick{} })
}

// Rendering and transfer consume part of the frame budget. Waiting a full
// interval after they finish slows the game's fixed-step simulation down.
func godotFrameDelay(started, now time.Time) time.Duration {
	return max(time.Duration(0), time.Second/60-now.Sub(started))
}

func (m godotPlayer) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
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
			shape := m.canvas.Shape()
			columns := min(m.width, 600/shape.Cols)
			rows := min(m.height-2, 360/shape.Rows)
			canvas := m.canvas
			if canvas.W != columns || canvas.H != rows*2 {
				canvas = sdk.NewCanvas(columns, rows*2, sdk.Black, shape)
			}
			keys := m.keys
			m.keys = nil
			m.busy = true
			started := time.Now()
			return m, func() tea.Msg {
				width, height := canvas.PixelSize()
				pixels, err := m.runtime.StepCanvas(keys, width, height, float64(shape.Rows)/float64(2*shape.Cols))
				return godotFrame{started: started, canvas: canvas, pixels: pixels, err: err}
			}
		}
		return m, godotNextTick(time.Now())
	case godotFrame:
		m.busy = false
		m.err = msg.err
		if msg.err == nil {
			m.canvas = msg.canvas
			copy(m.canvas.Pix(), msg.pixels)
		}
		return m, godotNextTick(msg.started)
	}
	return m, nil
}

func (m godotPlayer) View() tea.View {
	content := ""
	if m.width < 72 || m.height < 22 {
		content = "Godot terminal preview needs at least 72 columns × 22 rows."
	} else {
		content = m.canvas.Render() + "\n" + safeGodotText(m.runtime.Title) + " · Ctrl+P pause · Ctrl+C exit"
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
	case "esc":
		return special + 1
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

// Benchmark reports warmed engine exchange and cell conversion; it does not
// include the terminal emulator's painting or the player's 60 Hz pacing.
func benchmarkGodot(runtime *godot.Runtime, canvas *sdk.Canvas, frames, warmup int, input string, machine bool) error {
	events, err := loadGodotReplay(input, frames+warmup)
	if err != nil {
		return err
	}
	width, height := canvas.PixelSize()
	shape := canvas.Shape()
	samples := make([]float64, 0, frames)
	engine := map[string]float64{}
	cellMS := 0.0
	ansiBytes := 0
	for frame := 1; frame <= frames+warmup; frame++ {
		var keys []godot.KeyEvent
		for _, event := range events {
			if event.Frame == frame {
				keys = append(keys, event.KeyEvent)
			}
		}
		started := time.Now()
		pixels, err := runtime.StepCanvas(keys, width, height, float64(shape.Rows)/float64(2*shape.Cols))
		if err != nil {
			return err
		}
		elapsed := float64(time.Since(started).Microseconds()) / 1000
		if frame <= warmup {
			continue
		}
		samples = append(samples, elapsed)
		for key, value := range runtime.LastFrameTimes {
			engine[strings.TrimSuffix(key, "_us")] += float64(value) / 1000 / float64(frames)
		}
		copy(canvas.Pix(), pixels)
		started = time.Now()
		rendered := canvas.Render()
		cellMS += float64(time.Since(started).Microseconds()) / 1000 / float64(frames)
		ansiBytes += len(rendered)
	}
	sort.Float64s(samples)
	mean := 0.0
	for _, sample := range samples {
		mean += sample / float64(frames)
	}
	if machine {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "operation": "benchmark", "runtime": "godot-terminal-framebuffer-v1", "godot": godot.EngineVersion, "frames": frames, "warmup": warmup, "width": width, "height": height, "exchange_mean_ms": mean, "exchange_p95_ms": samples[(len(samples)-1)*95/100], "cell_render_mean_ms": cellMS, "ansi_bytes_per_frame": ansiBytes / frames, "engine_mean_ms": engine})
	}
	fmt.Printf("Godot frame exchange %.2f ms mean, %.2f ms p95; terminal cells %.2f ms; %d ANSI bytes/frame\n", mean, samples[(len(samples)-1)*95/100], cellMS, ansiBytes/frames)
	return nil
}
