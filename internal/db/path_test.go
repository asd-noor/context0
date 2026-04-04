package db_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	db "context0/internal/db"
)

// ── CodeMapDBName ─────────────────────────────────────────────────────────────

func TestCodeMapDBNameEmpty(t *testing.T) {
	got := db.CodeMapDBName("")
	want := "codemap-ctx0.sqlite"
	if got != want {
		t.Fatalf("CodeMapDBName(%q) = %q, want %q", "", got, want)
	}
}

func TestCodeMapDBNameBareName(t *testing.T) {
	got := db.CodeMapDBName("myrepo")
	want := "myrepo-ctx0.sqlite"
	if got != want {
		t.Fatalf("CodeMapDBName(%q) = %q, want %q", "myrepo", got, want)
	}
}

func TestCodeMapDBNameBareNameWithDashes(t *testing.T) {
	got := db.CodeMapDBName("my-cool-repo")
	want := "my-cool-repo-ctx0.sqlite"
	if got != want {
		t.Fatalf("CodeMapDBName(%q) = %q, want %q", "my-cool-repo", got, want)
	}
}

func TestCodeMapDBNamePathWithSeparator(t *testing.T) {
	// Any path that contains os.PathSeparator should produce an encoded name.
	dir := t.TempDir() // always an absolute path with separators on all OSes
	got := db.CodeMapDBName(dir)

	// Must end with -ctx0.sqlite
	if !strings.HasSuffix(got, "-ctx0.sqlite") {
		t.Fatalf("CodeMapDBName(path) = %q — expected suffix -ctx0.sqlite", got)
	}
	// Must not contain os.PathSeparator (separators replaced with '=')
	if strings.ContainsRune(got, filepath.Separator) {
		t.Fatalf("CodeMapDBName(path) = %q — should not contain path separators", got)
	}
	// Must contain '=' (confirming encoding happened)
	abs, _ := filepath.Abs(dir)
	if strings.ContainsRune(abs, filepath.Separator) && !strings.ContainsRune(got, '=') {
		t.Fatalf("CodeMapDBName(path) = %q — expected '=' encoding of path separators", got)
	}
}

func TestCodeMapDBNameTwoDifferentPathsProduceDifferentNames(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	if db.CodeMapDBName(dir1) == db.CodeMapDBName(dir2) {
		t.Fatalf("CodeMapDBName(%q) == CodeMapDBName(%q): expected distinct DB names", dir1, dir2)
	}
}

func TestCodeMapDBNameBareNameVsPath(t *testing.T) {
	// "myrepo" (bare) and "/some/path/myrepo" (full path) must differ so that
	// they use independent database files.
	bare := db.CodeMapDBName("myrepo")
	path := db.CodeMapDBName("/some/path/myrepo")
	if bare == path {
		t.Fatalf("bare name %q and path %q produced the same DB name %q", "myrepo", "/some/path/myrepo", bare)
	}
}

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
