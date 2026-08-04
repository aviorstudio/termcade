// Package engine is the host side of the arcade: it lists games (builtin or
// installed) and shields the shell from their failures. The game contract
// itself lives in the public sdk module.
package engine

import "github.com/aviorstudio/termcade/sdk"

// Registration lets the shell list and lazily instantiate games. New returns
// an error when instantiation can fail (e.g. loading an installed plugin);
// builtin games return nil.
//
// A Registration with Err set is a broken install: the menu lists it dimmed
// with the reason and refuses to launch it. Discovery failures become these
// instead of crashes.
type Registration struct {
	Info sdk.Info
	// New instantiates the game for a run. The cell shape is a launch-time
	// choice (the player can retoggle between runs), and wasm guests size
	// their pixel buffers from it, so it must reach instantiation rather
	// than being captured at discovery.
	New func(shape sdk.CellShape) (sdk.Game, error)
	Err error
	// Installed marks a game loaded from the games directory rather than
	// compiled into the arcade.
	Installed bool
	// Version is the package version from this game's manifest. It is not on
	// sdk.Info because a guest has no business knowing which build of itself is
	// running; the arcade needs it to report a run and to notice that the
	// marketplace has moved on.
	Version string
}
