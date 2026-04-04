// Package cmdexport implements the `context0 export` subcommand.
package cmdexport

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"context0/internal/archive"
	"context0/internal/db"
)

func NewCmd(projectDir *string) *cobra.Command {
	var outputPath string

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export project databases to a .tar.gz archive",
		Long: `Export all context0 databases for a project into a single .tar.gz archive.

The archive contains every file in the project's data directory
($HOME/.context0/<project>/) except PID files, storing only base file names
so the archive is portable across machines.`,

		RunE: func(cmd *cobra.Command, args []string) error {
			return run(*projectDir, outputPath)
		},
	}

	cmd.Flags().StringVarP(&outputPath, "output", "o", ".",
		`Output path: a directory (auto-generates "<basename>-ctx0-<timestamp>.tar.gz") or an explicit file path`)

	return cmd
}

func run(projectDir, outputPath string) error {
	dataDir, err := db.ProjectDir(projectDir)
	if err != nil {
		return fmt.Errorf("export: resolve project dir: %w", err)
	}

	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return fmt.Errorf("export: read data dir %q: %w", dataDir, err)
	}

	var files []string
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".pid") {
			continue
		}
		files = append(files, filepath.Join(dataDir, e.Name()))
	}

	if len(files) == 0 {
		return fmt.Errorf("export: no database files found in %q — has this project been used yet?", dataDir)
	}

	info, err := os.Stat(outputPath)
	if err == nil && info.IsDir() {
		ts := time.Now().Format("20060102-150405")
		outputPath = filepath.Join(outputPath, fmt.Sprintf("%s-ctx0-%s.tar.gz", filepath.Base(projectDir), ts))
	}

	if err := archive.Write(outputPath, files); err != nil {
		return fmt.Errorf("export: %w", err)
	}

	abs, _ := filepath.Abs(outputPath)
	fmt.Printf("exported %d file(s) → %s\n", len(files), abs)
	return nil
}
