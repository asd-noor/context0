// Package cmdimport implements the `context0 import` subcommand, which
// restores per-project databases from a .tar.gz archive produced by `context0
// export`.
package cmdimport

import (
	"fmt"

	"github.com/spf13/cobra"

	"context0/internal/archive"
	"context0/internal/db"
)

// NewCmd returns the `import` cobra.Command.
func NewCmd(projectDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "import <archive.tar.gz>",
		Short: "Import project databases from an export archive",
		Long: `Import context0 databases for a project from a .tar.gz archive
previously created by 'context0 export'.

Before overwriting any files the current state is snapshotted to:
  ~/.context0/backup/<encoded-project>/<timestamp>.tar.gz

The archive entries are then extracted into the project's data directory
($HOME/.context0/<encoded-project>/), overwriting existing files.

Only regular files are extracted; directory entries and PID files are ignored.`,

		Args: cobra.ExactArgs(1),

		RunE: func(cmd *cobra.Command, args []string) error {
			return run(*projectDir, args[0])
		},
	}
}

func run(projectDir, archivePath string) error {
	dataDir, err := db.ProjectDir(projectDir)
	if err != nil {
		return fmt.Errorf("import: resolve project dir: %w", err)
	}

	snapshotPath, err := archive.Snapshot(dataDir)
	if err != nil {
		return fmt.Errorf("import: snapshot: %w", err)
	}
	if snapshotPath != "" {
		fmt.Printf("snapshot → %s\n", snapshotPath)
	}

	n, err := archive.Extract(dataDir, archivePath)
	if err != nil {
		return fmt.Errorf("import: %w", err)
	}

	fmt.Printf("%d file(s) restored → %s\n", n, dataDir)
	return nil
}
