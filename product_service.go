package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/aviorstudio/termcade/internal/plugin"
	"github.com/aviorstudio/termcade/internal/registry"
	"github.com/aviorstudio/termcade/internal/scores"
	"github.com/aviorstudio/termcade/internal/shell"
	"github.com/aviorstudio/termcade/manifest"
)

type productBackend struct {
	store     registry.TUIStore
	configErr error
	runtime   *plugin.Runtime
	scores    *scores.Store
	mu        sync.Mutex
}

func newProductServices(rt *plugin.Runtime, st *scores.Store) *shell.ProductServices {
	store, err := registry.NewTUIStore()
	p := &productBackend{store: store, configErr: err, runtime: rt, scores: st}
	return &shell.ProductServices{Request: p.request, Sync: p.sync}
}

func (p *productBackend) client(ctx context.Context, required bool) (*registry.Client, *registry.Session, error) {
	if p.configErr != nil {
		return nil, nil, p.configErr
	}
	s, err := p.store.Load()
	if err != nil {
		return nil, nil, err
	}
	if required && s == nil {
		return nil, nil, registry.ErrLoginRequired
	}
	token := ""
	if s != nil {
		token = s.Token
	}
	return registry.New(p.store.Registry, token).WithContext(ctx), s, nil
}

func (p *productBackend) snapshot(ctx context.Context) shell.ProductSnapshot {
	var out shell.ProductSnapshot
	if dir, err := plugin.GamesDir(); err == nil {
		out.Installed = plugin.Games(p.runtime, dir, nil)
	}
	if p.configErr != nil {
		out.AccountError = p.configErr.Error()
		return out
	}
	// Catalog is public: never attach account credentials to public reads.
	catalog, err := registry.New(p.store.Registry, "").WithContext(ctx).Catalog()
	if err != nil {
		out.CatalogError = err.Error()
	} else {
		out.Catalog = catalog
	}
	c, s, err := p.client(ctx, false)
	if err != nil {
		out.AccountError = err.Error()
		return out
	}
	if s == nil {
		return out
	}
	out.SignedIn = true
	out.CredentialID = s.CredentialID
	out.Account = registry.Me{Email: s.Email, Username: s.Username}
	me, err := c.MeContext(ctx)
	if err != nil {
		out.AccountError = err.Error()
		if errors.Is(err, registry.ErrLoginRequired) {
			out.SignedIn = false
		}
		return out
	}
	out.Account = me
	out.Library, err = c.Library()
	out.LibraryKnown = err == nil
	if err != nil {
		out.LibraryError = err.Error()
	}
	out.Activity, _ = c.Activity()
	return out
}

func gameParts(id string) (string, string, error) {
	a, b, ok := strings.Cut(id, "/")
	if !ok || a == "" || b == "" || strings.ContainsAny(a+b, `\.`) || strings.Contains(b, "/") {
		return "", "", fmt.Errorf("game id must be author/slug")
	}
	return a, b, nil
}

func (p *productBackend) request(ctx context.Context, req shell.ProductRequest) (shell.ProductReply, error) {
	// Pairing waits without holding the session lock. It returns credentials to
	// the model; only an accepted current-generation result may request saving.
	if req.Kind == "pair-start" || req.Kind == "pair-wait" {
		if p.configErr != nil {
			return shell.ProductReply{}, p.configErr
		}
		if err := p.store.CanPair(); err != nil {
			return shell.ProductReply{}, err
		}
		c := registry.New(p.store.Registry, "").WithContext(ctx)
		if req.Kind == "pair-start" {
			round, err := c.StartDevice(ctx, "Termcade TUI")
			return shell.ProductReply{Round: round, PairingURL: p.store.PairingURL}, err
		}
		s, err := c.CompleteDevice(ctx, req.Round)
		return shell.ProductReply{Session: s}, err
	}
	if req.Kind == "open-url" {
		return shell.ProductReply{}, openProductURL(req.ID)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return shell.ProductReply{}, err
	}
	if req.Kind == "load" {
		snap := p.snapshot(ctx)
		return shell.ProductReply{Snapshot: &snap}, nil
	}
	if p.configErr != nil {
		return shell.ProductReply{}, p.configErr
	}
	var out shell.ProductReply
	var err error
	public := registry.New(p.store.Registry, "").WithContext(ctx)
	switch req.Kind {
	case "game":
		a, b, e := gameParts(req.ID)
		if e != nil {
			return out, e
		}
		g, e := public.Game(a, b)
		if e != nil {
			return out, e
		}
		out.Game = &g
		out.Days, err = public.PlayDays(a, b, time.Now().UTC().YearDay())
		if err != nil {
			out.Notice = "Play activity unavailable: " + err.Error()
			err = nil
		}
		return out, err
	case "owner":
		o, e := public.Owner(req.ID)
		if e != nil {
			return out, e
		}
		out.Owner = &o
		out.Days, err = public.PlayDays(req.ID, "", time.Now().UTC().YearDay())
		if err != nil {
			out.Notice = "Play activity unavailable: " + err.Error()
			err = nil
		}
		return out, err
	case "save-login":
		if err = p.store.Save(req.Session); err == nil && ctx.Err() != nil {
			_ = p.store.Clear()
			err = ctx.Err()
		}
	case "uninstall":
		a, b, e := gameParts(req.ID)
		if e != nil {
			return out, e
		}
		err = removeLocal(a, b)
	case "logout":
		c, s, e := p.client(ctx, false)
		if e != nil {
			out.Notice = "Local credentials cleared; could not identify remote credential."
		} else if s != nil {
			if s.CredentialID == "" {
				out.Notice = "Signed out locally. Legacy session has no credential ID; revoke it explicitly in session management."
			} else if e = c.RevokeCLICredential(s.CredentialID); e != nil {
				out.Notice = "Signed out locally; remote revocation failed. Use session management to revoke it."
			}
		}
		err = p.store.Clear()
	default:
		c, s, e := p.client(ctx, true)
		if e != nil {
			return out, e
		}
		switch req.Kind {
		case "add", "remove", "install":
			a, b, e := gameParts(req.ID)
			if e != nil {
				return out, e
			}
			switch req.Kind {
			case "add":
				err = c.LibraryAdd(a, b)
			case "remove":
				err = c.LibraryRemove(a, b)
				if errors.Is(err, registry.ErrNotFound) {
					err = nil
				}
			case "install":
				// Membership is independent of installation. Play may install only
				// an account game; local-only play never calls this path.
				owned, e := c.Library()
				if e != nil {
					return out, e
				}
				found := false
				for _, g := range owned {
					if g.ID == req.ID {
						found = true
					}
				}
				if !found {
					return out, fmt.Errorf("add this game to your library first")
				}
				dir, e := plugin.GamesDir()
				if e != nil {
					return out, e
				}
				if _, e = os.Stat(filepath.Join(dir, a, b)); e == nil {
					return out, fmt.Errorf("a local copy already exists; refresh Library instead of overwriting it")
				} else if !errors.Is(e, os.ErrNotExist) {
					return out, e
				}
				file, e := c.Download(a, b)
				if e != nil {
					return out, e
				}
				defer os.Remove(file)
				raw, e := os.ReadFile(file)
				if e != nil {
					return out, e
				}
				if e = ctx.Err(); e != nil {
					return out, e
				}
				pkg, e := manifest.ReadPackage(raw)
				if e != nil {
					return out, e
				}
				if pkg.Manifest.Game.ID != req.ID {
					return out, fmt.Errorf("downloaded package identity does not match the selected game")
				}
				_, _, err = installPackageBytesContext(ctx, raw)
			}
		case "username-check":
			_, taken, e := c.HandleTaken(req.Value)
			if e != nil {
				return out, e
			}
			if taken {
				out.Notice = "Handle is taken"
			} else {
				out.Notice = "Handle is available"
			}
			return out, nil
		case "username":
			_, err = c.SetUsername(req.Value)
		case "org-create":
			_, err = c.CreateOrg(req.ID, req.Value, req.Extra)
		case "members":
			out.Members, err = c.Members(req.ID)
			return out, err
		case "member-set":
			err = c.AddMember(req.ID, req.Value, req.Admin)
		case "member-remove":
			err = c.RemoveMember(req.ID, req.Value)
		case "org-delete":
			err = c.DeleteOrg(req.ID)
		case "sessions":
			out.Credentials, err = c.CLICredentials()
			return out, err
		case "session-revoke":
			err = c.RevokeCLICredential(req.ID)
			if err == nil && req.ID == s.CredentialID {
				err = p.store.Clear()
				out.Notice = "This device was revoked; signed out"
			}
		case "account-delete":
			err = c.DeleteAccount()
			if err == nil {
				err = p.store.Clear()
			}
		default:
			return out, fmt.Errorf("unsupported TUI action")
		}
	}
	if err != nil {
		return out, err
	}
	if out.Notice == "" {
		out.Notice = "Done"
	}
	snap := p.snapshot(ctx)
	out.Snapshot = &snap
	return out, nil
}

func (p *productBackend) sync(ctx context.Context) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if ctx.Err() != nil {
		return ""
	}
	c, s, err := p.client(ctx, false)
	if err != nil || s == nil {
		return ""
	}
	sent, err := flushRuns(c, p.scores)
	if err == nil {
		err = pullActivity(c, p.scores)
	}
	if sent > 0 {
		p.scores.Save()
	}
	if errors.Is(err, registry.ErrLoginRequired) {
		return "Session expired; sign in from Settings"
	}
	return syncMessage(sent, err)
}

// Only explicit UI actions call this. Never execute a shell command string or
// allow file/javascript/custom-scheme URLs supplied by a public profile.
func openProductURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw != "" && !strings.Contains(raw, ":") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("only HTTP(S) links can be opened")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", raw)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", raw)
	default:
		cmd = exec.Command("xdg-open", raw)
	}
	if err = cmd.Start(); err != nil {
		return fmt.Errorf("could not open a browser; copy the displayed URL instead")
	}
	finished := make(chan error, 1)
	go func() { finished <- cmd.Wait() }()
	select {
	case err := <-finished:
		if err != nil {
			return fmt.Errorf("the browser launcher failed; copy the displayed URL instead")
		}
		return nil
	case <-time.After(time.Second):
		return nil
	}
}
