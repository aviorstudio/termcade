package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/aviorstudio/termcade/internal/engine"
	"github.com/aviorstudio/termcade/internal/plugin"
	"github.com/aviorstudio/termcade/internal/registry"
	"github.com/aviorstudio/termcade/internal/scores"
	"github.com/aviorstudio/termcade/internal/shell"
)

// newMarketplace wires the arcade's marketplace screens to the registry and
// the local install machinery. Every hook is TUI-safe: no printing, errors
// returned for the shell to display.
func newMarketplace(rt *plugin.Runtime, st *scores.Store) *shell.Marketplace {
	anonClient := func() *registry.Client {
		session, _ := registry.LoadSession()
		token := ""
		if session != nil {
			token = session.Token
		}
		return registry.New(registry.URL(session), token)
	}

	reload := func() []engine.Registration {
		return discoverGames(rt)
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
			if err != nil {
				return err
			}
			// Browsing is anonymous; taking a game home is not. The footer
			// names the key that fixes this.
			if session == nil {
				return fmt.Errorf("sign in to install — press l")
			}
			author, slug, ok := strings.Cut(id, "/")
			if !ok {
				return fmt.Errorf("bad game id %q", id)
			}
			client := registry.New(registry.URL(session), session.Token)
			// Account state is the source of truth; the local package is a cache
			// which sync can restore if this download is interrupted.
			if err := client.LibraryAdd(author, slug); err != nil {
				return err
			}
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
			return nil
		},

		Remove: func(id string) error {
			author, slug, ok := strings.Cut(id, "/")
			if !ok {
				return fmt.Errorf("bad game id %q", id)
			}
			if err := syncLibraryRemove(author, slug); err != nil {
				return err
			}
			return removeLocal(author, slug)
		},

		Account: func() (string, bool) {
			session, err := registry.LoadSession()
			if err != nil || session == nil {
				return "", false
			}
			return session.Email, true
		},

		SignOut: registry.ClearSession,
		Reload:  reload,

		// Sync is the whole of continuity from the shell's side: queued runs
		// out, the account's record in, and the catalog's versions back so the
		// library can say what has moved on.
		//
		// Nothing here reports a failure. An arcade with no account and no
		// network is a supported way to use this, not an error state, and the
		// catalog is fetched separately from the account half so being signed
		// out still gets update markers.
		Sync: func() (string, map[string]string) {
			sent, err := syncActivity(st)
			if err == nil && sent > 0 {
				// Only worth the disk write when something actually changed.
				st.Save()
			}
			return syncMessage(sent, err), latestVersions()
		},
	}
}

// latestVersions asks the catalog what each game currently publishes. It is
// anonymous — browsing always is — so an arcade nobody has signed into still
// learns that its copy of a game is behind.
//
// A failure is an empty map rather than an error: not knowing means saying
// nothing, which is what the library does with an absent entry.
func latestVersions() map[string]string {
	session, _ := registry.LoadSession()
	games, err := registry.New(registry.URL(session), "").Games()
	if err != nil {
		return nil
	}
	out := make(map[string]string, len(games))
	for _, g := range games {
		if g.HasPackage && g.Version != "" {
			out[g.ID] = g.Version
		}
	}
	return out
}
