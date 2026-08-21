package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestScaffoldRequiresReleasedSDK pins the acceptance criterion: a new
// scaffold requires the released SDK v0.0.2, resolved like any other
// consumer — never a workspace path and never an unreleased version.
func TestScaffoldRequiresReleasedSDK(t *testing.T) {
	if scaffoldSDK != "v0.0.2" {
		t.Fatalf("scaffoldSDK = %s, want v0.0.2", scaffoldSDK)
	}
	if !strings.Contains(goModTmpl, "require github.com/aviorstudio/termcade/sdk "+scaffoldSDK+"\n") {
		t.Fatalf("go.mod template does not require sdk %s:\n%s", scaffoldSDK, goModTmpl)
	}
}

// TestScaffoldedGameBuildsAsAReleasedConsumer is the released-consumer gate
// for the scaffold: a generated project in a temporary directory outside
// this repository must resolve only released modules, build with GOWORK=off,
// and package into a WASI .tcade that passes the same ABI validation an
// install would do. The preload runs against a test-owned GOMODCACHE with
// explicit proxy/checksum policy, verified against this repository's go.sum;
// the consumer phase then runs with GOPROXY=off against that same isolated
// cache, so it proves a stranger's offline build works and cannot silently
// fall back to ambient module state or to the workspace the way v0.0.3 did.
func TestScaffoldedGameBuildsAsAReleasedConsumer(t *testing.T) {
	// Preload the released SDK into the module cache, verified against this
	// repository's go.sum, so the generated project below resolves offline.
	// The consumer gate owns its module cache: a fresh GOMODCACHE means the
	// generated project can only ever resolve what the preload below fetched,
	// never ambient state from a shared cache.
	cache := t.TempDir()

	// Preload the released SDK into the test-owned cache, resolved through
	// the public proxy and verified against this repository's go.sum
	// (GOWORK=off makes the repo's go.mod the preload's context). Proxy and
	// checksum policy are explicit rather than inherited. -modcacherw keeps
	// the test-owned cache writable so t.TempDir can remove it.
	preload := exec.Command("go", "mod", "download", "github.com/aviorstudio/termcade/sdk")
	preload.Env = append(os.Environ(),
		"GOWORK=off",
		"GOMODCACHE="+cache,
		"GOPROXY=https://proxy.golang.org,direct",
		"GOSUMDB=sum.golang.org",
		"GOFLAGS=-modcacherw",
	)
	if out, err := preload.CombinedOutput(); err != nil {
		t.Fatalf("preload released sdk: %v\n%s", err, out)
	}

	// Prove the preload landed in the isolated cache before trusting the
	// consumer phase below to run offline.
	zip := filepath.Join(cache, "cache", "download", "github.com", "aviorstudio",
		"termcade", "sdk", "@v", scaffoldSDK+".zip")
	if _, err := os.Stat(zip); err != nil {
		t.Fatalf("isolated cache missing %s: %v", zip, err)
	}

	// Everything after this point resolves offline from the isolated cache,
	// outside the workspace.
	t.Setenv("GOWORK", "off")
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOMODCACHE", cache)

	dir := filepath.Join(t.TempDir(), "pong")
	if err := cmdDevNew([]string{"test/pong", dir}); err != nil {
		t.Fatalf("dev new: %v", err)
	}

	run := func(name string, args ...string) string {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	run("go", "mod", "tidy")

	// Only released modules: no replace may be in effect, and the SDK must
	// resolve to exactly the pinned release.
	mods := run("go", "list", "-m",
		"-f", `{{.Path}} {{.Version}} {{with .Replace}}REPLACED{{end}}`, "all")
	if strings.Contains(mods, "REPLACED") {
		t.Fatalf("generated project resolves through a replace:\n%s", mods)
	}
	if !strings.Contains(mods, "github.com/aviorstudio/termcade/sdk "+scaffoldSDK+" ") {
		t.Fatalf("sdk did not resolve to released %s:\n%s", scaffoldSDK, mods)
	}

	// dev build compiles the WASI plugin and ABI-validates it the way an
	// install would.
	if err := cmdDevBuild([]string{dir}); err != nil {
		t.Fatalf("dev build: %v", err)
	}
	pkg := filepath.Join(dir, "build", "pong.tcade")
	if info, err := os.Stat(pkg); err != nil || info.Size() == 0 {
		t.Fatalf("expected non-empty %s, stat err: %v", pkg, err)
	}
}
