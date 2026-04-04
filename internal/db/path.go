// Package db provides shared utilities for resolving per-project SQLite
// database paths under $HOME/.context0/<transformed-project-dir>/.
package db

import (
	"os"
	"path/filepath"
	"strings"
)

// CodeMapDBName returns the SQLite file name for the codemap index.
//
// When srcRoot is empty the default name "codemap.sqlite" is returned so that
// existing projects are unaffected. When srcRoot is provided its absolute path
// is encoded using the same separator-replacement scheme as ProjectDir
// (os.PathSeparator → '=') and ".sqlite" is appended, giving each distinct
// scan root its own independent database file within the project directory.
func CodeMapDBName(srcRoot string) string {
	if srcRoot == "" {
		return "codemap.sqlite"
	}
	abs, err := filepath.Abs(srcRoot)
	if err != nil {
		// Unreachable in practice; fall back to the default name.
		return "codemap.sqlite"
	}
	transformed := strings.ReplaceAll(abs, string(os.PathSeparator), "=")
	transformed = strings.TrimPrefix(transformed, "=")
	return transformed + ".sqlite"
}

// ProjectDir returns the path to the per-project context0 data directory.
// The project directory path is transformed by replacing os.PathSeparator
// with equals signs, matching the spec in AGENTS.md.
func ProjectDir(projectPath string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	abs, err := filepath.Abs(projectPath)
	if err != nil {
		return "", err
	}

	// Replace path separators with equals signs.
	transformed := strings.ReplaceAll(abs, string(os.PathSeparator), "=")
	// Trim any leading equals sign that results from an absolute path starting with /.
	transformed = strings.TrimPrefix(transformed, "=")

	dir := filepath.Join(home, ".context0", transformed)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// DBPath returns the full path for a named SQLite database file inside the
// per-project directory for projectPath.
func DBPath(projectPath, dbName string) (string, error) {
	dir, err := ProjectDir(projectPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, dbName), nil
}

// PIDPath returns the path to the codemap daemon PID file for projectPath.
func PIDPath(projectPath string) (string, error) {
	return DBPath(projectPath, "codemap.pid")
}
