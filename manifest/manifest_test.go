package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goodTOML = `
[game]
id = "acme/blaster"
name = "Blaster"
version = "0.1.0"

[requirements]
abi = 1
width = 40
height = 30

[controls]
left = "aim"
`

func TestParseValid(t *testing.T) {
	m, err := Parse([]byte(goodTOML))
	if err != nil {
		t.Fatal(err)
	}
	if m.Author() != "acme" || m.Slug() != "blaster" {
		t.Errorf("author/slug = %q/%q", m.Author(), m.Slug())
	}
	info := m.Info()
	if info.ID != "acme/blaster" || info.Title != "BLASTER" || info.PixelW != 40 || info.PixelH != 30 {
		t.Errorf("Info = %+v", info)
	}
	if !m.CompatibleABI() {
		t.Error("abi 1 reported incompatible")
	}
	if m.Controls["left"] != "aim" {
		t.Errorf("controls = %v", m.Controls)
	}
}

func TestParseRejects(t *testing.T) {
	cases := map[string]string{
		"no id":         strings.Replace(goodTOML, `id = "acme/blaster"`, "", 1),
		"bare id":       strings.Replace(goodTOML, `"acme/blaster"`, `"blaster"`, 1),
		"uppercase id":  strings.Replace(goodTOML, `"acme/blaster"`, `"Acme/Blaster"`, 1),
		"no version":    strings.Replace(goodTOML, `version = "0.1.0"`, "", 1),
		"no abi":        strings.Replace(goodTOML, "abi = 1", "", 1),
		"odd height":    strings.Replace(goodTOML, "height = 30", "height = 31", 1),
		"garbage":       "not toml {{{",
		"missing sizes": strings.Replace(strings.Replace(goodTOML, "width = 40", "", 1), "height = 30", "", 1),
		// Manifests come from strangers; display strings must be inert.
		"escape in name":    strings.Replace(goodTOML, `name = "Blaster"`, "name = \"Bl\x1b[31master\"", 1),
		"escape in control": strings.Replace(goodTOML, `left = "aim"`, "left = \"a\x1b[2Jim\"", 1),
		"name too long":     strings.Replace(goodTOML, `name = "Blaster"`, `name = "`+strings.Repeat("A", 33)+`"`, 1),
		"huge playfield":    strings.Replace(goodTOML, "width = 40", "width = 100000", 1),
		"tall playfield":    strings.Replace(goodTOML, "height = 30", "height = 1000", 1),
	}
	for name, raw := range cases {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestPackageRoundTrip(t *testing.T) {
	dir := t.TempDir()
	pkgPath := filepath.Join(dir, "blaster.tcade")
	wasm := append([]byte{0x00, 0x61, 0x73, 0x6d}, []byte("rest-of-module")...)

	if err := WritePackage(pkgPath, []byte(goodTOML), wasm); err != nil {
		t.Fatal(err)
	}
	p, err := OpenPackage(pkgPath)
	if err != nil {
		t.Fatal(err)
	}
	if p.Manifest.Game.ID != "acme/blaster" || string(p.Wasm) != string(wasm) {
		t.Error("package contents did not round-trip")
	}

	gamesDir := filepath.Join(dir, "games")
	dest, err := p.Install(gamesDir)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(gamesDir, "acme", "blaster")
	if dest != want {
		t.Errorf("installed to %s, want %s", dest, want)
	}
	for _, f := range []string{FileName, WasmName} {
		if _, err := os.Stat(filepath.Join(dest, f)); err != nil {
			t.Errorf("missing installed file %s: %v", f, err)
		}
	}

	// Reinstalling replaces cleanly.
	if _, err := p.Install(gamesDir); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
}

func TestPackageRejects(t *testing.T) {
	dir := t.TempDir()

	// Not a zip.
	if _, err := ReadPackage([]byte("junk")); err == nil {
		t.Error("junk bytes accepted as package")
	}

	// Zip without wasm magic.
	p := filepath.Join(dir, "bad.tcade")
	if err := WritePackage(p, []byte(goodTOML), []byte("MZ not wasm")); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenPackage(p); err == nil {
		t.Error("non-wasm module accepted")
	}

	// Zip with a broken manifest.
	p2 := filepath.Join(dir, "bad2.tcade")
	if err := WritePackage(p2, []byte("id = 3"), []byte{0x00, 0x61, 0x73, 0x6d}); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenPackage(p2); err == nil {
		t.Error("broken manifest accepted")
	}
}
