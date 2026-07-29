package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aviorstudio/termcade/internal/manifest"
	"github.com/aviorstudio/termcade/internal/plugin"
)

const usage = `termcade — an arcade in your terminal

usage:
  termcade                     play
  termcade add <src>           add a game from a .tcade file or URL
  termcade remove <id>         remove an added game (id is author/slug)
  termcade list                list builtin and added games

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
	case "dev":
		switch {
		case len(args) >= 2 && args[1] == "build":
			err = cmdDevBuild(args[2:])
		case len(args) >= 2 && args[1] == "new":
			err = cmdDevNew(args[2:])
		default:
			err = fmt.Errorf("unknown dev subcommand; try: termcade dev new <author/slug> · termcade dev build [dir]")
		}
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

func cmdAdd(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: termcade add <file-or-url>")
	}
	src := args[0]

	var raw []byte
	var err error
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

	pkg, err := manifest.ReadPackage(raw)
	if err != nil {
		return err
	}
	if !pkg.Manifest.CompatibleABI() {
		return fmt.Errorf("%s needs ABI v%d; this termcade speaks v%d",
			pkg.Manifest.Game.ID, pkg.Manifest.Requirements.ABI, 1)
	}
	// Compile before installing: a module that doesn't build or lacks the
	// termcade exports never reaches the games directory.
	rt := plugin.NewRuntime(context.Background())
	defer rt.Close()
	if _, err := rt.Compile(pkg.Manifest.Game.ID, pkg.Wasm); err != nil {
		return err
	}

	gamesDir, err := plugin.GamesDir()
	if err != nil {
		return err
	}
	dest, err := pkg.Install(gamesDir)
	if err != nil {
		return err
	}
	fmt.Printf("added %s %s → %s\n", pkg.Manifest.Game.ID, pkg.Manifest.Game.Version, dest)
	for _, b := range builtins() {
		if b.Info.ID == pkg.Manifest.Game.ID {
			fmt.Printf("note: %s shadows a builtin game — the added version will be played; `termcade remove %s` restores the builtin\n",
				pkg.Manifest.Game.ID, pkg.Manifest.Game.ID)
		}
	}
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

func cmdRemove(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: termcade remove <author/slug>")
	}
	author, slug, ok := strings.Cut(args[0], "/")
	if !ok || author == "" || slug == "" ||
		strings.ContainsAny(author, `\.`) || strings.ContainsAny(slug, `\.`) {
		return fmt.Errorf("id must look like author/slug")
	}
	gamesDir, err := plugin.GamesDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(gamesDir, author, slug)
	if _, err := os.Stat(filepath.Join(dir, manifest.FileName)); err != nil {
		return fmt.Errorf("%s is not in your arcade", args[0])
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	fmt.Printf("removed %s\n", args[0])
	return nil
}

func cmdList() error {
	rt := plugin.NewRuntime(context.Background())
	defer rt.Close()

	games := builtins()
	if gamesDir, err := plugin.GamesDir(); err == nil {
		games = plugin.Games(rt, gamesDir, games, pixelShape())
	}
	for _, g := range games {
		switch {
		case g.Err != nil:
			fmt.Printf("%-24s %-28s broken: %v\n", g.Info.Title, g.Info.ID, g.Err)
		case g.Installed:
			fmt.Printf("%-24s %-28s added\n", g.Info.Title, g.Info.ID)
		default:
			fmt.Printf("%-24s %-28s builtin\n", g.Info.Title, g.Info.ID)
		}
	}
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

	out := m.Slug() + ".tcade"
	if err := manifest.WritePackage(out, manifestRaw, wasm); err != nil {
		return err
	}
	fmt.Printf("built %s %s → %s\n", m.Game.ID, m.Game.Version, out)
	return nil
}
