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
	New  func() (sdk.Game, error)
	Err  error
	// Installed marks a game loaded from the games directory rather than
	// compiled into the arcade.
	Installed bool
}
