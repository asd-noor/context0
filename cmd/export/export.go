// Package export implements the `context0 export` subcommand, which bundles
// all per-project databases into a portable .tar.gz archive.
package export

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

// NewCmd returns the `export` cobra.Command.
// projectDir is a pointer to the project directory string that is populated by
// the persistent --project / -p flag on the root command.
func NewCmd(projectDir *string) *cobra.Command {
	var outputPath string

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export project databases to a .tar.gz archive",
		Long: `Export all context0 databases for a project into a single .tar.gz archive.

The archive contains every file stored in the project's data directory
($HOME/.context0/<project>/) except PID files, preserving only base
file names so the archive is portable across machines.`,

		RunE: func(cmd *cobra.Command, args []string) error {
			return run(*projectDir, outputPath)
		},
	}

	cmd.Flags().StringVarP(&outputPath, "output", "o", ".",
		`Output path: a directory (auto-generates "<basename>-ctx0-<timestamp>.tar.gz" inside it) or an explicit file path`)

	return cmd
}

// run performs the export.
func run(projectDir, outputPath string) error {
	// Resolve the data directory for this project.
	dataDir, err := db.ProjectDir(projectDir)
	if err != nil {
		return fmt.Errorf("export: resolve project dir: %w", err)
	}

	// Collect files to archive (everything except *.pid).
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return fmt.Errorf("export: read data dir %q: %w", dataDir, err)
	}

	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".pid") {
			continue
		}
		files = append(files, filepath.Join(dataDir, e.Name()))
	}

	if len(files) == 0 {
		return fmt.Errorf("export: no database files found in %q — has this project been used yet?", dataDir)
	}

	// If output is a directory, generate the archive filename inside it.
	info, err := os.Stat(outputPath)
	if err == nil && info.IsDir() {
		ts := time.Now().Format("20060102-150405")
		basename := filepath.Base(projectDir)
		outputPath = filepath.Join(outputPath, fmt.Sprintf("%s-ctx0-%s.tar.gz", basename, ts))
	}

	// Create the output file.
	outFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("export: create output file: %w", err)
	}
	defer outFile.Close()

	gw := gzip.NewWriter(outFile)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	// Write each database file into the archive.
	for _, srcPath := range files {
		if err := addFile(tw, srcPath); err != nil {
			return fmt.Errorf("export: %w", err)
		}
	}

	// Flush gzip and tar before printing the success message.
	if err := tw.Close(); err != nil {
		return fmt.Errorf("export: finalise tar: %w", err)
	}
	if err := gw.Close(); err != nil {
		return fmt.Errorf("export: finalise gzip: %w", err)
	}

	abs, _ := filepath.Abs(outputPath)
	fmt.Printf("exported %d file(s) → %s\n", len(files), abs)
	return nil
}

// addFile writes a single file into the tar archive using its base name as the
// entry path, so the archive is independent of the source directory layout.
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
	// Store only the base name — no directory prefix — for portability.
	hdr.Name = filepath.Base(srcPath)

	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("write tar header for %q: %w", srcPath, err)
	}

	if _, err := io.Copy(tw, f); err != nil {
		return fmt.Errorf("write tar body for %q: %w", srcPath, err)
	}

	return nil
}
