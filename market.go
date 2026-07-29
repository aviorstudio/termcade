package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/aviorstudio/termcade/internal/engine"
	"github.com/aviorstudio/termcade/internal/plugin"
	"github.com/aviorstudio/termcade/internal/registry"
	"github.com/aviorstudio/termcade/internal/shell"
	"github.com/aviorstudio/termcade/sdk"
)

// newMarketplace wires the arcade's marketplace screens to the registry and
// the local install machinery. Every hook is TUI-safe: no printing, errors
// returned for the shell to display.
func newMarketplace(rt *plugin.Runtime, shape sdk.CellShape) *shell.Marketplace {
	anonClient := func() *registry.Client {
		session, _ := registry.LoadSession()
		token := ""
		if session != nil {
			token = session.Token
		}
		return registry.New(registry.URL(session), token)
	}

	reload := func() []engine.Registration {
		games := builtins()
		if dir, err := plugin.GamesDir(); err == nil {
			games = plugin.Games(rt, dir, games, shape)
		}
		return games
	}

	return &shell.Marketplace{
		List: func() ([]shell.MarketGame, error) {
			games, err := anonClient().Games()
			if err != nil {
				return nil, err
			}
			out := make([]shell.MarketGame, 0, len(games))
			for _, g := range games {
				out = append(out, shell.MarketGame{
					ID:          g.ID,
					Name:        g.Name,
					Description: g.Description,
					Version:     g.Version,
					HasPackage:  g.HasPackage,
				})
			}
			return out, nil
		},

		Install: func(id string) error {
			session, err := registry.LoadSession()
			if err != nil || session == nil {
				return fmt.Errorf("sign in to add games")
			}
			author, slug, ok := strings.Cut(id, "/")
			if !ok {
				return fmt.Errorf("bad game id %q", id)
			}
			client := registry.New(registry.URL(session), session.Token)
			path, err := client.Download(author, slug)
			if err != nil {
				return err
			}
			defer os.Remove(path)
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if _, _, err := installPackageBytes(raw); err != nil {
				return err
			}
			// Library sync is best-effort; the game is already installed.
			_ = client.LibraryAdd(author, slug)
			return nil
		},

		Remove: func(id string) error {
			author, slug, ok := strings.Cut(id, "/")
			if !ok {
				return fmt.Errorf("bad game id %q", id)
			}
			if err := removeLocal(author, slug); err != nil {
				return err
			}
			_ = syncLibraryRemove(author, slug)
			return nil
		},

		Account: func() (string, bool) {
			session, err := registry.LoadSession()
			if err != nil || session == nil {
				return "", false
			}
			return session.Email, true
		},

		SignIn: func(email, password string) error {
			session, err := registry.New(registry.URL(nil), "").Login(email, password)
			if err != nil {
				return err
			}
			return registry.SaveSession(session)
		},

		SignUp: func(email, password string) error {
			session, err := registry.New(registry.URL(nil), "").Signup(email, password)
			if err != nil {
				return err
			}
			return registry.SaveSession(session)
		},

		SignOut: registry.ClearSession,
		Reload:  reload,
	}
}
