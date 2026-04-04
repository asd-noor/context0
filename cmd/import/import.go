// Package cmdimport implements the `context0 import` subcommand, which
// restores per-project databases from a .tar.gz archive produced by `context0
// export`.
package cmdimport

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"context0/internal/db"
)

// NewCmd returns the `import` cobra.Command.
// projectDir is a pointer to the project directory string that is populated by
// the persistent --project / -p flag on the root command.
func NewCmd(projectDir *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import <archive.tar.gz>",
		Short: "Restore project databases from a .tar.gz archive",
		Long: `Restore context0 databases for a project from a .tar.gz archive
previously created by 'context0 export'.

Before overwriting any files a safety backup is written to:
  ~/.context0/backup/<encoded-project>/<timestamp>.tar.gz

The archive entries are then extracted into the project's data directory
($HOME/.context0/<encoded-project>/), overwriting existing files.

Only regular files are extracted; directory entries and PID files are ignored.`,

		Args: cobra.ExactArgs(1),

		RunE: func(cmd *cobra.Command, args []string) error {
			return run(*projectDir, args[0])
		},
	}

	return cmd
}

// run performs the import.
func run(projectDir, archivePath string) error {
	// Resolve the data directory for this project (created if absent).
	dataDir, err := db.ProjectDir(projectDir)
	if err != nil {
		return fmt.Errorf("import: resolve project dir: %w", err)
	}

	// Snapshot any existing files before we overwrite them.
	if err := backup(dataDir); err != nil {
		return fmt.Errorf("import: backup: %w", err)
	}

	// Open the source archive.
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("import: open archive: %w", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("import: read gzip header: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)

	var restored int

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("import: read tar entry: %w", err)
		}

		// Only extract regular files.
		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		// Sanitise: use only the base name to prevent path traversal.
		name := filepath.Base(hdr.Name)
		if name == "" || name == "." {
			continue
		}

		// Skip PID files — they are meaningless on a different machine.
		if strings.HasSuffix(name, ".pid") {
			continue
		}

		destPath := filepath.Join(dataDir, name)
		if err := extractFile(tr, destPath); err != nil {
			return fmt.Errorf("import: extract %q: %w", name, err)
		}

		fmt.Printf("restored %s\n", name)
		restored++
	}

	fmt.Printf("\n%d file(s) restored → %s\n", restored, dataDir)
	return nil
}

// backup archives all non-PID files currently in dataDir into
// ~/.context0/backup/<encoded-name>/<timestamp>.tar.gz.
// If dataDir is empty (first-time import) the backup is skipped silently.
func backup(dataDir string) error {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		// dataDir may not exist yet — nothing to back up.
		return nil
	}

	var files []string
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".pid") {
			continue
		}
		files = append(files, filepath.Join(dataDir, e.Name()))
	}

	if len(files) == 0 {
		return nil // nothing to back up
	}

	// Build backup path: ~/.context0/backup/<encoded-name>/<timestamp>.tar.gz
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home dir: %w", err)
	}
	encodedName := filepath.Base(dataDir)
	backupDir := filepath.Join(home, ".context0", "backup", encodedName)
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}

	ts := time.Now().Format("20060102-150405")
	backupPath := filepath.Join(backupDir, ts+".tar.gz")

	out, err := os.Create(backupPath)
	if err != nil {
		return fmt.Errorf("create backup file: %w", err)
	}
	defer out.Close()

	gw := gzip.NewWriter(out)
	tw := tar.NewWriter(gw)

	for _, srcPath := range files {
		if err := addFile(tw, srcPath); err != nil {
			tw.Close()
			gw.Close()
			os.Remove(backupPath)
			return err
		}
	}

	if err := tw.Close(); err != nil {
		gw.Close()
		os.Remove(backupPath)
		return fmt.Errorf("finalise tar: %w", err)
	}
	if err := gw.Close(); err != nil {
		os.Remove(backupPath)
		return fmt.Errorf("finalise gzip: %w", err)
	}
	if err := out.Close(); err != nil {
		os.Remove(backupPath)
		return fmt.Errorf("close backup file: %w", err)
	}

	abs, _ := filepath.Abs(backupPath)
	fmt.Printf("backup → %s\n", abs)
	return nil
}

// addFile writes a single file into the tar archive using its base name only.
func addFile(tw *tar.Writer, srcPath string) error {
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
		return fmt.Errorf("write tar header for %q: %w", srcPath, err)
	}
	if _, err := io.Copy(tw, f); err != nil {
		return fmt.Errorf("write tar body for %q: %w", srcPath, err)
	}
	return nil
}

// extractFile writes the current tar entry to destPath atomically via a temp
// file, so a partial write never leaves a corrupt database on disk.
func extractFile(tr *tar.Reader, destPath string) error {
	dir := filepath.Dir(destPath)
	tmp, err := os.CreateTemp(dir, ".ctx0-import-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
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
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, destPath); err != nil {
		return fmt.Errorf("rename into place: %w", err)
	}

	ok = true
	return nil
}
