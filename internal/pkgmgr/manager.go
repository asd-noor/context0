// Package pkgmgr manages the download and installation of LSP language-server
// binaries on behalf of the codemap engine.
//
// Resolution priority (per binary):
//  1. Cached binary in the context0 install dir
//  2. Binary found on system $PATH
//  3. Auto-download via the package manager
package pkgmgr

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// installBase returns the base directory for context0-managed binaries:
// $HOME/.context0/bin/
func installBase() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".context0", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// binaryPath returns the full path to a managed binary.
func binaryPath(name string) (string, error) {
	base, err := installBase()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(base, name), nil
}

// Manager provides binary resolution and installation.
type Manager struct{}

// New creates a Manager.
func New() *Manager { return &Manager{} }

// ResolveBinary resolves the given binary name using the priority:
//  1. context0-managed install dir
//  2. system $PATH
//  3. auto-download (install)
func (m *Manager) ResolveBinary(ctx context.Context, name string) (string, error) {
	// 1. Check context0-managed install dir.
	cached, err := binaryPath(name)
	if err == nil {
		if _, statErr := os.Stat(cached); statErr == nil {
			return cached, nil
		}
	}

	// 2. Check system PATH.
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}

	// 3. Auto-download.
	installed, err := Install(ctx, name)
	if err != nil {
		return "", fmt.Errorf("pkgmgr: %s not found on PATH and auto-install failed: %w", name, err)
	}
	return installed, nil
}

// Install downloads and installs the named language-server binary.
// Returns the path to the installed binary.
func Install(ctx context.Context, name string) (string, error) {
	meta, ok := binaryMeta[name]
	if !ok {
		return "", fmt.Errorf("pkgmgr: no install metadata for %q", name)
	}
	return meta.install(ctx, name)
}
