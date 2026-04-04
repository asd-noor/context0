// Package archive provides shared helpers for writing and reading the
// .tar.gz archives used by the backup and recover commands.
package archive

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Write creates a .tar.gz archive at destPath containing files, stored by
// base name only (no directory prefix) so archives are portable across
// machines. On any error the partial output file is removed.
func Write(destPath string, files []string) error {
	out, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create %q: %w", destPath, err)
	}

	gw := gzip.NewWriter(out)
	tw := tar.NewWriter(gw)

	abort := func() {
		tw.Close()
		gw.Close()
		out.Close()
		os.Remove(destPath)
	}

	for _, srcPath := range files {
		if err := writeEntry(tw, srcPath); err != nil {
			abort()
			return err
		}
	}
	if err := tw.Close(); err != nil {
		gw.Close()
		out.Close()
		os.Remove(destPath)
		return fmt.Errorf("finalise tar: %w", err)
	}
	if err := gw.Close(); err != nil {
		out.Close()
		os.Remove(destPath)
		return fmt.Errorf("finalise gzip: %w", err)
	}
	return out.Close()
}

// Extract reads a .tar.gz archive and writes every regular non-PID file into
// dataDir, overwriting existing files atomically via temp-file + rename.
// Returns the number of files extracted.
func Extract(dataDir, archivePath string) (int, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return 0, fmt.Errorf("create data dir: %w", err)
	}

	f, err := os.Open(archivePath)
	if err != nil {
		return 0, fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return 0, fmt.Errorf("read gzip header: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	var n int

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return n, fmt.Errorf("read tar entry: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		// Sanitise: base name only, skip PID files.
		name := filepath.Base(hdr.Name)
		if name == "" || name == "." || strings.HasSuffix(name, ".pid") {
			continue
		}

		if err := extractEntry(tr, filepath.Join(dataDir, name)); err != nil {
			return n, fmt.Errorf("extract %q: %w", name, err)
		}
		n++
	}

	return n, nil
}

// BackupDir returns the directory where automatic snapshots for dataDir are
// stored: ~/.context0/backup/<base(dataDir)>.
// The directory is NOT created by this call.
func BackupDir(dataDir string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	return filepath.Join(home, ".context0", "backup", filepath.Base(dataDir)), nil
}

// Snapshot saves all non-PID files in dataDir to
// ~/.context0/backup/<base(dataDir)>/<timestamp>.tar.gz.
// Returns the absolute path of the created archive, or "" if there was
// nothing to back up (empty or non-existent dataDir).
func Snapshot(dataDir string) (string, error) {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return "", nil // dir doesn't exist yet — nothing to snapshot
	}

	var files []string
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".pid") {
			continue
		}
		files = append(files, filepath.Join(dataDir, e.Name()))
	}
	if len(files) == 0 {
		return "", nil
	}

	backupDir, err := BackupDir(dataDir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", fmt.Errorf("create backup dir: %w", err)
	}

	destPath := filepath.Join(backupDir, time.Now().Format("20060102-150405")+".tar.gz")
	if err := Write(destPath, files); err != nil {
		return "", err
	}

	abs, _ := filepath.Abs(destPath)
	return abs, nil
}

// writeEntry adds a single file to the tar archive using its base name only.
func writeEntry(tw *tar.Writer, srcPath string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open %q: %w", srcPath, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat %q: %w", srcPath, err)
	}

	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return fmt.Errorf("tar header for %q: %w", srcPath, err)
	}
	hdr.Name = filepath.Base(srcPath)

	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("write header for %q: %w", srcPath, err)
	}
	if _, err := io.Copy(tw, f); err != nil {
		return fmt.Errorf("write body for %q: %w", srcPath, err)
	}
	return nil
}

// extractEntry writes the current tar entry to destPath atomically.
func extractEntry(tr *tar.Reader, destPath string) error {
	tmp, err := os.CreateTemp(filepath.Dir(destPath), ".ctx0-recover-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()

	ok := false
	defer func() {
		if !ok {
			os.Remove(tmpName)
		}
	}()

	if _, err := io.Copy(tmp, tr); err != nil {
		tmp.Close()
		return fmt.Errorf("write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpName, destPath); err != nil {
		return fmt.Errorf("rename: %w", err)
	}

	ok = true
	return nil
}
