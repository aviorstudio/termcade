package manifest

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// maxWasmSize bounds how much of a package we're willing to unpack: a wasm
// module bigger than this is either corrupt or hostile.
const maxWasmSize = 64 << 20

// wasmMagic is the module preamble: "\0asm".
var wasmMagic = []byte{0x00, 0x61, 0x73, 0x6d}

// Package is an opened .tcade file: the parsed manifest plus the raw module.
type Package struct {
	Manifest *Manifest
	Wasm     []byte
}

// OpenPackage reads and validates a .tcade zip from disk.
func OpenPackage(path string) (*Package, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("opening package: %w", err)
	}
	defer zr.Close()
	return readPackage(&zr.Reader)
}

// ReadPackage reads and validates a .tcade zip from memory (e.g. a download).
func ReadPackage(raw []byte) (*Package, error) {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, fmt.Errorf("opening package: %w", err)
	}
	return readPackage(zr)
}

func readPackage(zr *zip.Reader) (*Package, error) {
	manifestRaw, err := zipFile(zr, FileName, 1<<20)
	if err != nil {
		return nil, err
	}
	m, err := Parse(manifestRaw)
	if err != nil {
		return nil, err
	}
	wasm, err := zipFile(zr, WasmName, maxWasmSize)
	if err != nil {
		return nil, err
	}
	if len(wasm) < len(wasmMagic) || !bytes.Equal(wasm[:len(wasmMagic)], wasmMagic) {
		return nil, fmt.Errorf("%s is not a wasm module", WasmName)
	}
	return &Package{Manifest: m, Wasm: wasm}, nil
}

func zipFile(zr *zip.Reader, name string, limit int64) ([]byte, error) {
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", name, err)
		}
		defer rc.Close()
		raw, err := io.ReadAll(io.LimitReader(rc, limit+1))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", name, err)
		}
		if int64(len(raw)) > limit {
			return nil, fmt.Errorf("%s exceeds the %d byte limit", name, limit)
		}
		return raw, nil
	}
	return nil, fmt.Errorf("package has no %s at its root", name)
}

// WritePackage zips a manifest and module into a .tcade at path.
func WritePackage(path string, manifestRaw, wasm []byte) error {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range []struct {
		name string
		raw  []byte
	}{{FileName, manifestRaw}, {WasmName, wasm}} {
		w, err := zw.Create(f.name)
		if err != nil {
			return err
		}
		if _, err := w.Write(f.raw); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// Install validates pkg and extracts it atomically under gamesDir, replacing
// any prior version of the same game. Layout:
// <gamesDir>/<author>/<slug>/{termcade.toml, game.wasm}.
func (p *Package) Install(gamesDir string) (string, error) {
	dest := filepath.Join(gamesDir, p.Manifest.Author(), p.Manifest.Slug())
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dest), "."+p.Manifest.Slug()+"-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	manifestRaw, err := toEncodedTOML(p.Manifest)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(tmp, FileName), manifestRaw, 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(tmp, WasmName), p.Wasm, 0o644); err != nil {
		return "", err
	}
	if err := os.RemoveAll(dest); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return "", err
	}
	return dest, nil
}

func toEncodedTOML(m *Manifest) ([]byte, error) {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(m); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
