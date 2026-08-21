package manifest

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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
	manifestF, wasmF, err := rootEntries(zr)
	if err != nil {
		return nil, err
	}
	manifestRaw, err := readEntry(manifestF, 1<<20)
	if err != nil {
		return nil, err
	}
	m, err := Parse(manifestRaw)
	if err != nil {
		return nil, err
	}
	wasm, err := readEntry(wasmF, maxWasmSize)
	if err != nil {
		return nil, err
	}
	if len(wasm) < len(wasmMagic) || !bytes.Equal(wasm[:len(wasmMagic)], wasmMagic) {
		return nil, fmt.Errorf("%s is not a wasm module", WasmName)
	}
	return &Package{Manifest: m, Wasm: wasm}, nil
}

// rootEntries enforces the package contract: exactly one termcade.toml and
// one game.wasm at the zip's root, and nothing else. A zip's central
// directory may carry duplicate names, nested paths and directory entries,
// and a .tcade comes from strangers, so each violation is its own explicit
// rejection rather than a silently ignored entry.
func rootEntries(zr *zip.Reader) (manifestF, wasmF *zip.File, err error) {
	for _, f := range zr.File {
		// zip's general-purpose flag bit 0 marks an encrypted entry. The
		// reader cannot decrypt one, so reject it by name instead of
		// letting it fail later as a read error or an empty file.
		if f.Flags&0x1 != 0 {
			return nil, nil, fmt.Errorf("package entry %q is encrypted", f.Name)
		}
		// A directory is also marked by external attributes, with no
		// trailing slash required — so a name match is not enough. The two
		// required entries must be regular files (FileInfo semantics, which
		// cover the MS-DOS directory bit and Unix mode bits alike); any
		// other type — directory, symlink, device — has no meaning in a
		// two-file package.
		if f.Name == FileName || f.Name == WasmName {
			if !f.FileInfo().Mode().IsRegular() {
				return nil, nil, fmt.Errorf("package entry %q is not a regular file", f.Name)
			}
		}
		switch f.Name {
		case FileName:
			if manifestF != nil {
				return nil, nil, fmt.Errorf("package has more than one %s", FileName)
			}
			manifestF = f
		case WasmName:
			if wasmF != nil {
				return nil, nil, fmt.Errorf("package has more than one %s", WasmName)
			}
			wasmF = f
		default:
			if strings.Contains(f.Name, "/") {
				return nil, nil, fmt.Errorf("package entry %q is not at the root: a .tcade contains only %s and %s", f.Name, FileName, WasmName)
			}
			return nil, nil, fmt.Errorf("unexpected package entry %q: a .tcade contains only %s and %s", f.Name, FileName, WasmName)
		}
	}
	if manifestF == nil {
		return nil, nil, fmt.Errorf("package has no %s at its root", FileName)
	}
	if wasmF == nil {
		return nil, nil, fmt.Errorf("package has no %s at its root", WasmName)
	}
	return manifestF, wasmF, nil
}

func readEntry(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", f.Name, err)
	}
	defer rc.Close()
	raw, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", f.Name, err)
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("%s exceeds the %d byte limit", f.Name, limit)
	}
	return raw, nil
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
