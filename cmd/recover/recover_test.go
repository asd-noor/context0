package cmdrecover

// latestArchive is unexported, so tests live in the same package.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── latestArchive ─────────────────────────────────────────────────────────────

// TestLatestArchiveNonExistentDir verifies that a missing directory returns a
// descriptive error.
func TestLatestArchiveNonExistentDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "no-such-dir")
	_, err := latestArchive(dir)
	if err == nil {
		t.Fatal("expected error for non-existent dir, got nil")
	}
	if !strings.Contains(err.Error(), "no backups found") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestLatestArchiveEmptyDir verifies that a directory with no .tar.gz files
// returns a descriptive error.
func TestLatestArchiveEmptyDir(t *testing.T) {
	dir := t.TempDir()
	_, err := latestArchive(dir)
	if err == nil {
		t.Fatal("expected error for empty dir, got nil")
	}
	if !strings.Contains(err.Error(), "no backups found") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestLatestArchiveIgnoresNonTarGz verifies that files without the .tar.gz
// suffix are ignored, and an error is returned when no valid archive exists.
func TestLatestArchiveIgnoresNonTarGz(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"backup.zip", "notes.txt", "data.tar"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte{}, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	_, err := latestArchive(dir)
	if err == nil {
		t.Fatal("expected error when only non-.tar.gz files present, got nil")
	}
}

// TestLatestArchiveSingleFile verifies that a directory with exactly one
// .tar.gz file returns that file's path.
func TestLatestArchiveSingleFile(t *testing.T) {
	dir := t.TempDir()
	name := "20240101-120000.tar.gz"
	if err := os.WriteFile(filepath.Join(dir, name), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := latestArchive(dir)
	if err != nil {
		t.Fatalf("latestArchive: %v", err)
	}
	want := filepath.Join(dir, name)
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestLatestArchivePicksLexicographicallyLast verifies that when multiple
// archives exist the lexicographically greatest name is selected. Because the
// filename format is YYYYMMDD-HHMMSS, lexicographic order equals chronological
// order.
func TestLatestArchivePicksLexicographicallyLast(t *testing.T) {
	dir := t.TempDir()
	archives := []string{
		"20240101-090000.tar.gz",
		"20240315-153000.tar.gz",
		"20240315-153001.tar.gz", // latest
		"20231231-235959.tar.gz",
	}
	for _, name := range archives {
		if err := os.WriteFile(filepath.Join(dir, name), []byte{}, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := latestArchive(dir)
	if err != nil {
		t.Fatalf("latestArchive: %v", err)
	}
	want := filepath.Join(dir, "20240315-153001.tar.gz")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestLatestArchiveIgnoresDirsWithTarGzSuffix verifies that subdirectories
// whose names end in .tar.gz are not mistakenly treated as archives.
func TestLatestArchiveIgnoresDirsWithTarGzSuffix(t *testing.T) {
	dir := t.TempDir()
	// Create a subdirectory that happens to end in .tar.gz.
	subdir := filepath.Join(dir, "fake.tar.gz")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Also create a real archive so we can tell which one wins.
	realName := "20240101-000000.tar.gz"
	if err := os.WriteFile(filepath.Join(dir, realName), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := latestArchive(dir)
	if err != nil {
		t.Fatalf("latestArchive: %v", err)
	}
	want := filepath.Join(dir, realName)
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
