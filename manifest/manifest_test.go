package manifest

import (
	"archive/zip"
	"bytes"
	"io/fs"
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

var goodWasm = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

type zipEntry struct {
	name      string
	body      []byte
	encrypted bool        // sets general-purpose flag bit 0 without encrypting
	mode      fs.FileMode // external attributes, e.g. fs.ModeDir without a "/" name
}

// rawZip builds a zip in memory with exactly the entries given, in order —
// including duplicate names (legal in zip, rejected by the package contract),
// encrypted flags and attribute-marked directories, none of which
// WritePackage can produce.
func rawZip(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		var w interface{ Write([]byte) (int, error) }
		var err error
		switch {
		case e.encrypted:
			fh := &zip.FileHeader{Name: e.name, Method: zip.Store}
			fh.Flags |= 0x1
			w, err = zw.CreateRaw(fh)
		case e.mode != 0:
			fh := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
			fh.SetMode(e.mode)
			w, err = zw.CreateHeader(fh)
		default:
			w, err = zw.Create(e.name)
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// A .tcade holds exactly one termcade.toml and one game.wasm at its root and
// nothing else; every deviation below must be rejected by name, never
// silently ignored.
func TestPackageEntryContract(t *testing.T) {
	cases := map[string]struct {
		entries []zipEntry
		want    string // substring of the rejection message
	}{
		"extra root entry": {
			entries: []zipEntry{{FileName, []byte(goodTOML), false, 0}, {WasmName, goodWasm, false, 0}, {"README.md", []byte("hi"), false, 0}},
			want:    `unexpected package entry "README.md"`,
		},
		"duplicate manifest": {
			entries: []zipEntry{{FileName, []byte(goodTOML), false, 0}, {WasmName, goodWasm, false, 0}, {FileName, []byte(goodTOML), false, 0}},
			want:    "package has more than one " + FileName,
		},
		"duplicate wasm": {
			entries: []zipEntry{{FileName, []byte(goodTOML), false, 0}, {WasmName, goodWasm, false, 0}, {WasmName, goodWasm, false, 0}},
			want:    "package has more than one " + WasmName,
		},
		"nested lookalike manifest": {
			entries: []zipEntry{{FileName, []byte(goodTOML), false, 0}, {WasmName, goodWasm, false, 0}, {"sub/" + FileName, []byte(goodTOML), false, 0}},
			want:    `"sub/` + FileName + `" is not at the root`,
		},
		"nested lookalike wasm": {
			entries: []zipEntry{{FileName, []byte(goodTOML), false, 0}, {WasmName, goodWasm, false, 0}, {"sub/" + WasmName, goodWasm, false, 0}},
			want:    `"sub/` + WasmName + `" is not at the root`,
		},
		"directory entry": {
			entries: []zipEntry{{FileName, []byte(goodTOML), false, 0}, {WasmName, goodWasm, false, 0}, {"sub/", nil, false, 0}},
			want:    `"sub/" is not at the root`,
		},
		// A directory is marked by external attributes too, not only by a
		// trailing slash: a required name carrying ModeDir (or any other
		// non-regular mode) is not a file, whatever its name says.
		"manifest marked as directory": {
			entries: []zipEntry{{FileName, []byte(goodTOML), false, fs.ModeDir | 0o755}, {WasmName, goodWasm, false, 0}},
			want:    `"` + FileName + `" is not a regular file`,
		},
		"wasm marked as directory": {
			entries: []zipEntry{{FileName, []byte(goodTOML), false, 0}, {WasmName, goodWasm, false, fs.ModeDir | 0o755}},
			want:    `"` + WasmName + `" is not a regular file`,
		},
		"wasm marked as symlink": {
			entries: []zipEntry{{FileName, []byte(goodTOML), false, 0}, {WasmName, goodWasm, false, fs.ModeSymlink | 0o777}},
			want:    `"` + WasmName + `" is not a regular file`,
		},
		"encrypted entry": {
			entries: []zipEntry{{FileName, []byte(goodTOML), false, 0}, {WasmName, goodWasm, true, 0}},
			want:    `"` + WasmName + `" is encrypted`,
		},
		"missing wasm": {
			entries: []zipEntry{{FileName, []byte(goodTOML), false, 0}},
			want:    "package has no " + WasmName + " at its root",
		},
		"empty archive": {
			entries: nil,
			want:    "package has no " + FileName + " at its root",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ReadPackage(rawZip(t, tc.entries...))
			if err == nil {
				t.Fatalf("accepted, want rejection containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err, tc.want)
			}
		})
	}

	// The contract's other side: exactly the two right entries pass it, in
	// either order.
	for _, entries := range [][]zipEntry{
		{{FileName, []byte(goodTOML), false, 0}, {WasmName, goodWasm, false, 0}},
		{{WasmName, goodWasm, false, 0}, {FileName, []byte(goodTOML), false, 0}},
	} {
		if _, err := ReadPackage(rawZip(t, entries...)); err != nil {
			t.Errorf("valid package %v rejected: %v", entries[0].name, err)
		}
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
