package shell

import (
	"context"

	"github.com/aviorstudio/termcade/internal/engine"
	"github.com/aviorstudio/termcade/internal/registry"
)

// ProductServices is the non-printing, cancellable boundary used by the full
// application shell. The older Marketplace hooks remain for game-only embeds.
type ProductServices struct {
	Request func(context.Context, ProductRequest) (ProductReply, error)
	Sync    func(context.Context) string
}

type ProductSnapshot struct {
	Catalog      []registry.Game
	Library      []registry.Game
	Installed    []engine.Registration
	Account      registry.Me
	SignedIn     bool
	LibraryKnown bool
	CredentialID string
	CatalogError string
	AccountError string
	LibraryError string
	Activity     []registry.Activity
}

type ProductRequest struct {
	Kind    string
	ID      string
	Value   string
	Extra   string
	Admin   bool
	Round   registry.DeviceRound
	Session registry.Session
}

type ProductReply struct {
	Snapshot    *ProductSnapshot
	Game        *registry.Game
	Owner       *registry.HandleOwner
	Days        []registry.PlayDay
	Members     []registry.Member
	Credentials []registry.CLICredential
	Round       registry.DeviceRound
	PairingURL  string
	Session     registry.Session
	Notice      string
}
