package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aviorstudio/termcade/internal/plugin"
	"github.com/aviorstudio/termcade/internal/registry"
	"github.com/aviorstudio/termcade/manifest"
)

const usage = `termcade — an arcade in your terminal

usage:
  termcade                     play (marketplace included — press m)
  termcade add <game>          add a game: author/slug from the marketplace,
                               or a .tcade file or URL (needs an account)
  termcade remove <id>         remove an added game (id is author/slug)
  termcade list                list the games in your arcade

  termcade publish <repo> <tag> [asset]
                               publish a release: point the marketplace at a
                               .tcade on one of your GitHub releases

  termcade signup [email]      create a marketplace account
  termcade login [email]       sign in (publishing and your account library)
  termcade logout              sign out

  termcade dev new <id> [dir]  start your own game (id is author/slug)
  termcade dev build [dir]     build a game directory into a .tcade package

This arcade is also the whole dev kit: dev new, hack, dev build, add,
play. See docs/sdk.md to get started.
`

// runCommand dispatches non-play subcommands; it reports whether it handled
// one (main falls through to the arcade otherwise).
func runCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	var err error
	switch args[0] {
	case "add", "install":
		err = cmdAdd(args[1:])
	case "remove", "uninstall":
		err = cmdRemove(args[1:])
	case "list":
		err = cmdList()
	case "login":
		err = cmdLogin(args[1:])
	case "signup":
		err = cmdSignup(args[1:])
	case "logout":
		err = cmdLogout()
	case "publish":
		err = cmdPublish(args[1:])
	case "dev":
		switch {
		case len(args) >= 2 && args[1] == "build":
			err = cmdDevBuild(args[2:])
		case len(args) >= 2 && args[1] == "new":
			err = cmdDevNew(args[2:])
		default:
			err = fmt.Errorf("unknown dev subcommand; try: termcade dev new <author/slug> · termcade dev build [dir]")
		}
	case "version", "-v", "--version":
		fmt.Println("termcade", resolveVersion())
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "termcade: unknown command %q\n\n%s", args[0], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "termcade:", err)
		os.Exit(1)
	}
	return true
}

// gameIDRe matches a marketplace id, e.g. "aviorstudio/brickough".
var gameIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*/[a-z0-9][a-z0-9-]*$`)

func cmdAdd(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: termcade add <author/slug | file | url>")
	}
	src := args[0]

	// Pinning is gone. Someone with the old spelling in their fingers or in a
	// script should be told that, not handed a missing-file error for what is
	// obviously a marketplace id. Before the account check, because this is
	// the command being wrong rather than the caller being anonymous — being
	// told to sign in and then told the syntax changed is two trips.
	if id, _, pinned := strings.Cut(src, "@"); pinned && gameIDRe.MatchString(id) {
		return fmt.Errorf("versions cannot be pinned — `termcade add %s` installs what %s currently ships", id, id)
	}

	session, err := registry.LoadSession()
	if err != nil {
		return err
	}
	// Every install goes through an account, whatever the source. The bundled
	// starter pack is what a signed-out arcade has to play; adding to it is
	// the thing an account is for.
	if session == nil {
		return fmt.Errorf("installing a game requires an account — run `termcade login` (or `termcade signup`)")
	}

	// Marketplace id → fetch through the registry and sync the library.
	if _, statErr := os.Stat(src); gameIDRe.MatchString(src) && statErr != nil {
		return addFromRegistry(session, src)
	}

	var raw []byte
	if strings.Contains(src, "://") {
		if !strings.HasPrefix(src, "https://") {
			return fmt.Errorf("refusing to download over %s; games install code — use https",
				strings.SplitN(src, "://", 2)[0])
		}
		raw, err = download(src)
	} else {
		raw, err = os.ReadFile(src)
	}
	if err != nil {
		return err
	}
	return installPackage(raw)
}

// addFromRegistry installs a marketplace id. The session is never nil: cmdAdd
// turns a signed-out install away before it gets here.
func addFromRegistry(session *registry.Session, id string) error {
	author, slug, _ := strings.Cut(id, "/")
	client := registry.New(registry.URL(session), session.Token)

	path, err := client.Download(author, slug)
	if errors.Is(err, registry.ErrLoginRequired) {
		return fmt.Errorf("your session has expired — run `termcade login`")
	}
	if err != nil {
		return err
	}
	defer os.Remove(path)

	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := installPackage(raw); err != nil {
		return err
	}
	// Library sync is best-effort: the game is already installed locally.
	if err := client.LibraryAdd(author, slug); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not add %s to your library: %v\n", id, err)
	}
	return nil
}

// installPackageBytes validates and installs a .tcade without printing, so
// the TUI can call it too: manifest parses, ABI is speakable, the wasm
// compiles and exports the termcade ABI — then an atomic extract into the
// games directory.
func installPackageBytes(raw []byte) (*manifest.Package, string, error) {
	pkg, err := manifest.ReadPackage(raw)
	if err != nil {
		return nil, "", err
	}
	if !pkg.Manifest.CompatibleABI() {
		return nil, "", fmt.Errorf("%s needs ABI v%d; this termcade speaks v%d",
			pkg.Manifest.Game.ID, pkg.Manifest.Requirements.ABI, 1)
	}
	rt := plugin.NewRuntime(context.Background())
	defer rt.Close()
	if _, err := rt.Compile(pkg.Manifest.Game.ID, pkg.Wasm); err != nil {
		return nil, "", err
	}

	gamesDir, err := plugin.GamesDir()
	if err != nil {
		return nil, "", err
	}
	dest, err := pkg.Install(gamesDir)
	if err != nil {
		return nil, "", err
	}
	return pkg, dest, nil
}

func installPackage(raw []byte) error {
	pkg, dest, err := installPackageBytes(raw)
	if err != nil {
		return err
	}
	fmt.Printf("added %s %s → %s\n", pkg.Manifest.Game.ID, pkg.Manifest.Game.Version, dest)
	return nil
}

func download(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 128<<20))
}

// removeLocal deletes an installed game's directory. Silent, TUI-safe.
func removeLocal(author, slug string) error {
	if author == "" || slug == "" ||
		strings.ContainsAny(author, `\.`) || strings.ContainsAny(slug, `\.`) {
		return fmt.Errorf("id must look like author/slug")
	}
	gamesDir, err := plugin.GamesDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(gamesDir, author, slug)
	if _, err := os.Stat(filepath.Join(dir, manifest.FileName)); err != nil {
		return fmt.Errorf("%s/%s is not in your arcade", author, slug)
	}
	return os.RemoveAll(dir)
}

// syncLibraryRemove best-effort mirrors a removal to the signed-in library.
func syncLibraryRemove(author, slug string) error {
	session, err := registry.LoadSession()
	if err != nil || session == nil {
		return nil // purely local when signed out
	}
	client := registry.New(registry.URL(session), session.Token)
	err = client.LibraryRemove(author, slug)
	if err == nil || errors.Is(err, registry.ErrLoginRequired) ||
		strings.Contains(err.Error(), "not in your library") {
		return nil
	}
	return err
}

func cmdRemove(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: termcade remove <author/slug>")
	}
	author, slug, ok := strings.Cut(args[0], "/")
	if !ok {
		return fmt.Errorf("id must look like author/slug")
	}
	if err := removeLocal(author, slug); err != nil {
		return err
	}
	fmt.Printf("removed %s\n", args[0])
	if err := syncLibraryRemove(author, slug); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not update your library: %v\n", err)
	}
	return nil
}

func cmdList() error {
	rt := plugin.NewRuntime(context.Background())
	defer rt.Close()

	for _, g := range discoverGames(rt) {
		if g.Err != nil {
			fmt.Printf("%-24s %-28s broken: %v\n", g.Info.Title, g.Info.ID, g.Err)
			continue
		}
		fmt.Printf("%-24s %-28s installed\n", g.Info.Title, g.Info.ID)
	}
	return nil
}

// cmdPublish points the marketplace at a package on a GitHub release. The
// registry fetches it, validates it against the same manifest rules the
// arcade uses, and records its digest — so what is published is decided by
// the package, not by anything asserted here.
//
// This is the signed-in path. Publishing from an author's own CI, with a
// scoped key rather than a session, is a separate credential and is not
// built yet.
func cmdPublish(args []string) error {
	if len(args) < 2 || len(args) > 3 {
		return fmt.Errorf("usage: termcade publish <repo-url> <tag> [asset]")
	}
	session, err := registry.LoadSession()
	if err != nil {
		return err
	}
	if session == nil {
		return fmt.Errorf("publishing requires an account — run `termcade login` (or `termcade signup`)")
	}

	repo, tag := args[0], args[1]
	asset := ""
	if len(args) == 3 {
		asset = args[2]
	} else {
		// Default to what `dev build` names its output, read from the game
		// directory we are standing in.
		raw, err := os.ReadFile(manifest.FileName)
		if err != nil {
			return fmt.Errorf("no asset name given, and no %s here to infer one from", manifest.FileName)
		}
		m, err := manifest.Parse(raw)
		if err != nil {
			return err
		}
		asset = m.Slug() + ".tcade"
	}

	out, err := registry.New(registry.URL(session), session.Token).Publish(repo, tag, asset)
	if err != nil {
		return err
	}
	fmt.Printf("published %s %s\n", out.ID, out.Version)
	fmt.Printf("  from   %s @ %s (%s)\n", out.Repo, out.Tag, out.Asset)
	fmt.Printf("  sha256 %s\n", out.SHA256)
	return nil
}

func cmdDevBuild(args []string) error {
	dir := "."
	if len(args) == 1 {
		dir = args[0]
	} else if len(args) > 1 {
		return fmt.Errorf("usage: termcade dev build [dir]")
	}

	manifestRaw, err := os.ReadFile(filepath.Join(dir, manifest.FileName))
	if err != nil {
		return fmt.Errorf("a game directory needs a %s: %w", manifest.FileName, err)
	}
	m, err := manifest.Parse(manifestRaw)
	if err != nil {
		return err
	}
	wasmPkg := filepath.Join(dir, "cmd", "wasm")
	if _, err := os.Stat(wasmPkg); err != nil {
		return fmt.Errorf("a game directory needs a cmd/wasm entrypoint: %w", err)
	}

	tmp, err := os.MkdirTemp("", "termcade-build-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	wasmOut := filepath.Join(tmp, manifest.WasmName)

	build := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasmOut, "./"+filepath.ToSlash(filepath.Join("cmd", "wasm")))
	build.Dir = dir
	build.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("go build failed: %w\n%s", err, out)
	}
	wasm, err := os.ReadFile(wasmOut)
	if err != nil {
		return err
	}

	// The same validation an install would do, so authors find out now.
	rt := plugin.NewRuntime(context.Background())
	defer rt.Close()
	if _, err := rt.Compile(m.Game.ID, wasm); err != nil {
		return err
	}

	// Output lands inside the game directory, never the invoker's cwd; build
	// artifacts stay next to what built them (and out of version control).
	buildDir := filepath.Join(dir, "build")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return err
	}
	out := filepath.Join(buildDir, m.Slug()+".tcade")
	if err := manifest.WritePackage(out, manifestRaw, wasm); err != nil {
		return err
	}
	fmt.Printf("built %s %s → %s\n", m.Game.ID, m.Game.Version, out)
	return nil
}
