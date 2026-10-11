// Package godot implements the developer-only Godot terminal target. Godot
// executes native project code, so these packages are deliberately separate
// from sandboxed .tcade installs and the public marketplace validator.
package godot

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const EngineVersion = "4.7.2.stable.official.ed1daf0bf"

//go:embed addon/*
var addon embed.FS

// Binary resolves an explicitly selected or PATH-provided pinned engine.
func Binary(ctx context.Context) (string, error) {
	bin := os.Getenv("GODOT_BIN")
	if bin == "" {
		bin = "godot"
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return "", fmt.Errorf("install Godot %s or set GODOT_BIN: %w", EngineVersion, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil || strings.TrimSpace(string(output)) != EngineVersion {
		return "", fmt.Errorf("Godot must be %s; got %q (%v)", EngineVersion, strings.TrimSpace(string(output)), err)
	}
	return path, nil
}

// InstallAddon exposes the same real export platform used by the CLI in a
// consuming Godot editor. Existing edits to the addon are never overwritten.
func InstallAddon(ctx context.Context, bin, project string) error {
	if _, err := os.Stat(filepath.Join(project, "project.godot")); err != nil {
		return fmt.Errorf("expected a Godot project: %w", err)
	}
	if err := writeAddon(project); err != nil {
		return err
	}
	log, err := runEngine(ctx, bin, "--headless", "--path", project,
		"--script", "res://addons/termcade/setup.gd")
	if err != nil {
		return err
	}
	if !strings.Contains(log, "TERMCADE SETUP OK") {
		return fmt.Errorf("Godot setup did not complete: %s", log)
	}
	return nil
}

func writeAddon(project string) error {
	entries, _ := fs.ReadDir(addon, "addon")
	dir := filepath.Join(project, "addons", "termcade")
	// Validate every destination before writing any of them.
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		data, _ := addon.ReadFile("addon/" + entry.Name())
		old, err := os.ReadFile(path)
		if err == nil && string(old) != string(data) {
			return fmt.Errorf("refusing to overwrite edited addon file %s", path)
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, entry := range entries {
		data, _ := addon.ReadFile("addon/" + entry.Name())
		if err := os.WriteFile(filepath.Join(dir, entry.Name()), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Export imports and exports a disposable project copy through Godot's real
// Termcade EditorExportPlatformExtension. No graphical export template is used.
// A failed validation leaves both the source project and destination untouched.
func Export(ctx context.Context, bin, project, destination string) error {
	if filepath.Ext(destination) != ".tgd" {
		return fmt.Errorf("Godot terminal packages use .tgd, not .tcade")
	}
	project, err := filepath.Abs(project)
	if err != nil {
		return err
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return fmt.Errorf("destination already exists or is inaccessible: %s", destination)
	}
	stage, err := os.MkdirTemp("", "termcade-godot-export-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := copyProject(project, stage); err != nil {
		return err
	}
	if err := InstallAddon(ctx, bin, stage); err != nil {
		return err
	}
	if _, err := runEngine(ctx, bin, "--headless", "--quiet", "--editor", "--path", stage, "--import"); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(destination), ".termcade-*.tgd")
	if err != nil {
		return err
	}
	temp.Close()
	defer os.Remove(temp.Name())
	if _, err := runEngine(ctx, bin, "--headless", "--quiet", "--path", stage,
		"--export-release", "Termcade", temp.Name()); err != nil {
		return err
	}
	info, err := os.Stat(temp.Name())
	if err != nil || info.Size() == 0 || info.Size() > maxPackageBytes {
		return fmt.Errorf("Godot did not produce a bounded terminal package")
	}
	// Link is atomic and refuses a destination created during the export.
	// Both paths share a directory/filesystem; never replace someone else's file.
	return os.Link(temp.Name(), destination)
}

const maxPackageBytes = 256 << 20

func copyProject(source, destination string) error {
	var total int64
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil || relative == "." {
			return err
		}
		if strings.HasPrefix(entry.Name(), ".") || (entry.IsDir() && (entry.Name() == "build" || entry.Name() == "dist")) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("project symlinks are unsupported: %s", relative)
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("project contains a non-regular file: %s", relative)
		}
		total += info.Size()
		if total > maxPackageBytes {
			return fmt.Errorf("project exceeds 256 MiB")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// Godot may report a script error and still exit zero. Treat the actual error
// output as failure, as the fleet's shared Godot test runner does.
type engineLog struct {
	mu     sync.Mutex
	text   string
	carry  string
	failed bool
}

func (log *engineLog) Write(data []byte) (int, error) {
	log.mu.Lock()
	defer log.mu.Unlock()
	combined := log.carry + string(data)
	for _, marker := range []string{"SCRIPT ERROR:", "ERROR:", "TERMCADE EXPORT ERROR:"} {
		if strings.Contains(combined, marker) {
			log.failed = true
		}
	}
	log.carry = combined[max(0, len(combined)-64):]
	log.text += string(data)
	log.text = log.text[max(0, len(log.text)-(32<<10)):]
	return len(data), nil
}

func (log *engineLog) snapshot() (string, bool) {
	log.mu.Lock()
	defer log.mu.Unlock()
	return log.text, log.failed
}

func runEngine(ctx context.Context, bin string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.WaitDelay = time.Second
	state, err := os.MkdirTemp("", "termcade-godot-tool-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(state)
	cmd.Env = append(os.Environ(), "XDG_DATA_HOME="+state, "XDG_CONFIG_HOME="+state, "XDG_CACHE_HOME="+state)
	log := &engineLog{}
	cmd.Stdout, cmd.Stderr = log, log
	err = cmd.Run()
	output, failed := log.snapshot()
	if err != nil || failed {
		return output, fmt.Errorf("Godot command failed (%v): %s", err, output)
	}
	return output, nil
}
