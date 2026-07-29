package sdk

// Simulation timing is part of the game contract: the shell always steps the
// simulation at exactly TPS fixed ticks per second. Games may count ticks or
// multiply rates by Dt; both are equivalent and deterministic.
const (
	TPS = 60
	Dt  = 1.0 / TPS
)

// ABIVersion is the wasm plugin ABI major version. A guest reports it via
// termcade_abi; a host refuses to run a guest whose version it doesn't speak.
const ABIVersion = 1

// Info describes a game's identity and its fixed logical playfield size.
// PixelW is terminal columns; PixelH is pixels (2 per terminal row, so even).
type Info struct {
	ID     string // namespaced "author/slug" key, e.g. "aviorstudio/brickough"
	Title  string // display name, e.g. "BRICKOUGH"
	PixelW int
	PixelH int
}

type Status int

const (
	StatusRunning Status = iota
	StatusGameOver
)

// HUDField is one labeled value on the status line, e.g. {"SCORE", "001200"}.
// Accent fields render bright; the rest render dim.
type HUDField struct {
	Label  string
	Value  string
	Accent bool
}

// HUD is the structured status line shown beneath the canvas. The shell owns
// all styling; games supply plain text only. Control characters are stripped
// before display, so a game cannot smuggle escape sequences into the frame.
type HUD struct {
	Fields []HUDField
	Hint   string // trailing dim hint, e.g. "space to launch"
}

// Game is the contract every arcade game implements. The shell owns the
// tick loop and canvas; games only mutate state and draw.
type Game interface {
	Info() Info
	// Reset starts a fresh run: score 0, full lives, first level.
	Reset()
	// HandleKey receives one call per keypress event.
	HandleKey(k Key)
	// HandleKeyUp receives one call per key-release event, on terminals that
	// report them. Terminals that do not simply never call it.
	HandleKeyUp(k Key)
	// Update advances the simulation by exactly one tick (1/TPS seconds) and
	// reports whether the run has ended.
	Update() Status
	// Draw renders the current state onto a canvas the shell has cleared.
	Draw(c *Canvas)
	// Score is valid at any time; the shell reads it after game over.
	Score() int
	// HUD returns the structured status line shown beneath the canvas.
	HUD() HUD
}
