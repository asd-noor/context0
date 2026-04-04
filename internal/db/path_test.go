package db_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	db "context0/internal/db"
)

func TestProjectDirTransformsPathWithEqualsSeparator(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	projectPath := filepath.Join(t.TempDir(), "nested", "project")
	abs, err := filepath.Abs(projectPath)
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}

	dir, err := db.ProjectDir(projectPath)
	if err != nil {
		t.Fatalf("ProjectDir: %v", err)
	}

	transformed := strings.ReplaceAll(abs, string(os.PathSeparator), "=")
	transformed = strings.TrimPrefix(transformed, "=")
	want := filepath.Join(home, ".context0", transformed)

	if dir != want {
		t.Fatalf("ProjectDir() = %q, want %q", dir, want)
	}
	if strings.Contains(dir, "=") == false && strings.Contains(abs, string(os.PathSeparator)) {
		t.Fatalf("ProjectDir() = %q, should contain '=' as path separator replacement", dir)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("os.Stat(%q): %v", dir, err)
	}
}
