package sdk

// Key is the small input vocabulary games understand; the shell translates
// terminal key events into these. The numeric values are frozen — they cross
// the wasm plugin boundary — so new keys may only be appended.
type Key int

const (
	KeyNone  Key = 0
	KeyLeft  Key = 1
	KeyRight Key = 2
	KeyUp    Key = 3
	KeyDown  Key = 4
	KeyA     Key = 5 // primary action (space, z)
	KeyB     Key = 6 // secondary action (x)
	KeyStart Key = 7 // enter
)

// DefaultHoldTicks is the auto-repeat fallback window for KeyTracker, in
// ticks; unused on terminals that report key releases.
const DefaultHoldTicks = 8

// KeyTracker answers "is this key held right now?" on terminals that disagree
// about whether they will tell us.
//
// Terminals speaking the Kitty keyboard protocol send real release events, so
// held state is exact. Everywhere else there are no releases at all, and the
// only signal is auto-repeat — which stalls for the OS repeat delay (commonly
// ~500ms) after the initial press. Those terminals fall back to treating a key
// as held for holdTicks frames after its most recent press.
//
// The mode is discovered, not configured: the first release event we ever see
// proves the terminal reports them, and the tracker switches to exact state
// permanently.
type KeyTracker struct {
	lastPress map[Key]int
	down      map[Key]bool
	frame     int
	holdTicks int
	exact     bool
}

func NewKeyTracker(holdTicks int) *KeyTracker {
	return &KeyTracker{
		lastPress: make(map[Key]int),
		down:      make(map[Key]bool),
		holdTicks: holdTicks,
	}
}

// Press records a key event; call from Game.HandleKey.
func (t *KeyTracker) Press(k Key) {
	t.lastPress[k] = t.frame
	t.down[k] = true
}

// Release records a key-up event; call from Game.HandleKeyUp. Receiving one at
// all is what tells the tracker it can stop guessing.
func (t *KeyTracker) Release(k Key) {
	t.exact = true
	t.down[k] = false
}

// Tick advances the frame counter; call once per Game.Update.
func (t *KeyTracker) Tick() {
	t.frame++
}

// Held reports whether k is currently held.
func (t *KeyTracker) Held(k Key) bool {
	if t.exact {
		return t.down[k]
	}
	f, ok := t.lastPress[k]
	return ok && t.frame-f < t.holdTicks
}

// Exact reports whether held state comes from real release events rather than
// the auto-repeat fallback.
func (t *KeyTracker) Exact() bool { return t.exact }

// Reset clears all key state, e.g. when respawning or unpausing. It keeps the
// discovered input mode, which is a property of the terminal, not the run.
func (t *KeyTracker) Reset() {
	clear(t.lastPress)
	clear(t.down)
}
