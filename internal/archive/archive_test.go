package archive_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"context0/internal/archive"
)

// writeTempFiles creates named files with the given content in dir and returns
// their absolute paths.
func writeTempFiles(t *testing.T, dir string, files map[string]string) []string {
	t.Helper()
	var paths []string
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("writeTempFiles: %v", err)
		}
		paths = append(paths, p)
	}
	return paths
}

// ── Write / Extract ───────────────────────────────────────────────────────────

// TestWriteExtractRoundTrip writes several files into an archive then extracts
// them and verifies every byte is preserved.
func TestWriteExtractRoundTrip(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	arc := filepath.Join(t.TempDir(), "test.tar.gz")

	want := map[string]string{
		"alpha.db":  "alpha contents",
		"beta.db":   "beta contents",
		"gamma.txt": "gamma contents",
	}
	paths := writeTempFiles(t, src, want)

	if err := archive.Write(arc, paths); err != nil {
		t.Fatalf("Write: %v", err)
	}

	n, err := archive.Extract(dst, arc)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if n != len(want) {
		t.Errorf("Extract: got %d files, want %d", n, len(want))
	}

	for name, wantContent := range want {
		data, err := os.ReadFile(filepath.Join(dst, name))
		if err != nil {
			t.Errorf("missing extracted file %q: %v", name, err)
			continue
		}
		if string(data) != wantContent {
			t.Errorf("file %q: got %q, want %q", name, data, wantContent)
		}
	}
}

// TestWriteStoresBaseNameOnly verifies that archive entries carry only the base
// file name, not the full source path, so archives are portable.
func TestWriteStoresBaseNameOnly(t *testing.T) {
	src := t.TempDir()
	arc := filepath.Join(t.TempDir(), "out.tar.gz")
	paths := writeTempFiles(t, src, map[string]string{"only-base.db": "x"})

	if err := archive.Write(arc, paths); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Extract into a completely different directory — must succeed.
	dst := t.TempDir()
	if _, err := archive.Extract(dst, arc); err != nil {
		t.Fatalf("Extract into different dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "only-base.db")); err != nil {
		t.Errorf("expected only-base.db in dest dir: %v", err)
	}
}

// TestExtractSkipsPIDFiles verifies that .pid files in the archive are silently
// ignored and not written to the destination directory.
func TestExtractSkipsPIDFiles(t *testing.T) {
	src := t.TempDir()
	arc := filepath.Join(t.TempDir(), "out.tar.gz")
	paths := writeTempFiles(t, src, map[string]string{
		"real.db":    "keep me",
		"daemon.pid": "12345",
	})

	if err := archive.Write(arc, paths); err != nil {
		t.Fatalf("Write: %v", err)
	}

	dst := t.TempDir()
	n, err := archive.Extract(dst, arc)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 extracted file (PID skipped), got %d", n)
	}
	if _, err := os.Stat(filepath.Join(dst, "daemon.pid")); err == nil {
		t.Error("PID file should not have been extracted")
	}
}

// TestExtractCreatesDataDir verifies that Extract creates the destination
// directory if it does not already exist.
func TestExtractCreatesDataDir(t *testing.T) {
	src := t.TempDir()
	arc := filepath.Join(t.TempDir(), "out.tar.gz")
	paths := writeTempFiles(t, src, map[string]string{"a.db": "data"})

	if err := archive.Write(arc, paths); err != nil {
		t.Fatalf("Write: %v", err)
	}

	newDir := filepath.Join(t.TempDir(), "brand", "new", "dir")
	if _, err := archive.Extract(newDir, arc); err != nil {
		t.Fatalf("Extract into non-existent dir: %v", err)
	}
	if _, err := os.Stat(newDir); err != nil {
		t.Errorf("data dir was not created: %v", err)
	}
}

// TestExtractOverwritesExistingFile verifies that extracting a file that
// already exists in the destination replaces it atomically.
func TestExtractOverwritesExistingFile(t *testing.T) {
	src := t.TempDir()
	arc := filepath.Join(t.TempDir(), "out.tar.gz")
	paths := writeTempFiles(t, src, map[string]string{"db.sqlite": "new content"})

	if err := archive.Write(arc, paths); err != nil {
		t.Fatalf("Write: %v", err)
	}

	dst := t.TempDir()
	// Pre-place an old version.
	if err := os.WriteFile(filepath.Join(dst, "db.sqlite"), []byte("old content"), 0o644); err != nil {
		t.Fatalf("pre-place: %v", err)
	}

	if _, err := archive.Extract(dst, arc); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dst, "db.sqlite"))
	if string(data) != "new content" {
		t.Errorf("expected overwritten content, got %q", data)
	}
}

// TestWriteEmptyFileList verifies that writing an empty file list produces a
// valid (empty) archive that can be extracted without error.
func TestWriteEmptyFileList(t *testing.T) {
	arc := filepath.Join(t.TempDir(), "empty.tar.gz")
	if err := archive.Write(arc, nil); err != nil {
		t.Fatalf("Write(nil): %v", err)
	}

	n, err := archive.Extract(t.TempDir(), arc)
	if err != nil {
		t.Fatalf("Extract empty archive: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 extracted files, got %d", n)
	}
}

// TestWriteAbortsCleansUpOnError verifies that if Write fails mid-way (because
// one source file does not exist), it removes the partial output file.
func TestWriteAbortsCleansUpOnError(t *testing.T) {
	src := t.TempDir()
	arc := filepath.Join(t.TempDir(), "partial.tar.gz")

	good := filepath.Join(src, "good.db")
	if err := os.WriteFile(good, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(src, "does-not-exist.db")

	_ = archive.Write(arc, []string{good, missing})

	if _, err := os.Stat(arc); err == nil {
		t.Error("expected partial archive to be cleaned up, but it still exists")
	}
}

// ── BackupDir ─────────────────────────────────────────────────────────────────

// TestBackupDirStructure verifies the returned path ends with the correct
// components relative to the home directory.
func TestBackupDirStructure(t *testing.T) {
	// Point HOME at a temp dir so we don't touch the real home.
	home := t.TempDir()
	t.Setenv("HOME", home)

	dataDir := filepath.Join(home, ".context0", "some=project=path")
	got, err := archive.BackupDir(dataDir)
	if err != nil {
		t.Fatalf("BackupDir: %v", err)
	}

	want := filepath.Join(home, ".context0", "backup", "some=project=path")
	if got != want {
		t.Errorf("BackupDir: got %q, want %q", got, want)
	}
}

// ── Snapshot ──────────────────────────────────────────────────────────────────

// TestSnapshotNonExistentDirIsNoop verifies that Snapshot on a non-existent
// directory returns ("", nil) without creating anything.
func TestSnapshotNonExistentDirIsNoop(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := archive.Snapshot(filepath.Join(home, "does-not-exist"))
	if err != nil {
		t.Fatalf("Snapshot on missing dir: %v", err)
	}
	if path != "" {
		t.Errorf("expected empty path, got %q", path)
	}
}

// TestSnapshotEmptyDirIsNoop verifies that Snapshot on a directory with no
// eligible files returns ("", nil).
func TestSnapshotEmptyDirIsNoop(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dataDir := filepath.Join(home, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}

	path, err := archive.Snapshot(dataDir)
	if err != nil {
		t.Fatalf("Snapshot on empty dir: %v", err)
	}
	if path != "" {
		t.Errorf("expected empty path, got %q", path)
	}
}

// TestSnapshotSkipsPIDFiles verifies that .pid files are not included in the
// snapshot archive.
func TestSnapshotSkipsPIDFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dataDir := filepath.Join(home, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTempFiles(t, dataDir, map[string]string{
		"memory-ctx0.sqlite": "db content",
		"codemap.pid":        "9999",
	})

	snapshotPath, err := archive.Snapshot(dataDir)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshotPath == "" {
		t.Fatal("expected a snapshot path, got empty string")
	}

	// Extract into fresh dir and check PID was excluded.
	dst := t.TempDir()
	n, err := archive.Extract(dst, snapshotPath)
	if err != nil {
		t.Fatalf("Extract snapshot: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 file in snapshot (PID excluded), got %d", n)
	}
}

// TestSnapshotCreatesArchive verifies the happy path: files are snapshotted,
// the returned path exists, and the archive round-trips correctly.
func TestSnapshotCreatesArchive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dataDir := filepath.Join(home, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTempFiles(t, dataDir, map[string]string{
		"memory-ctx0.sqlite": "memory data",
		"agenda-ctx0.sqlite": "agenda data",
	})

	snapshotPath, err := archive.Snapshot(dataDir)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshotPath == "" {
		t.Fatal("expected non-empty snapshot path")
	}
	if !strings.HasSuffix(snapshotPath, ".tar.gz") {
		t.Errorf("snapshot path should end in .tar.gz, got %q", snapshotPath)
	}
	if _, err := os.Stat(snapshotPath); err != nil {
		t.Errorf("snapshot archive does not exist: %v", err)
	}

	dst := t.TempDir()
	n, err := archive.Extract(dst, snapshotPath)
	if err != nil {
		t.Fatalf("Extract snapshot: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 files in snapshot, got %d", n)
	}
}
