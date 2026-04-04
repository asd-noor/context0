// Package cmdbackup implements the `context0 backup` subcommand, which
// snapshots all per-project databases into the automatic backup directory:
//
//	~/.context0/backup/<encoded-project>/<timestamp>.tar.gz
package cmdbackup

import (
	"fmt"

	"github.com/spf13/cobra"

	"context0/internal/archive"
	"context0/internal/db"
)

// NewCmd returns the `backup` cobra.Command.
func NewCmd(projectDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "backup",
		Short: "Snapshot project databases to the automatic backup directory",
		Long: `Snapshot all context0 databases for a project into:

  ~/.context0/backup/<encoded-project>/<timestamp>.tar.gz

PID files are excluded. Use 'context0 recover' to restore the latest snapshot.`,

		RunE: func(cmd *cobra.Command, args []string) error {
			return run(*projectDir)
		},
	}
}

func run(projectDir string) error {
	dataDir, err := db.ProjectDir(projectDir)
	if err != nil {
		return fmt.Errorf("backup: resolve project dir: %w", err)
	}

	snapshotPath, err := archive.Snapshot(dataDir)
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	if snapshotPath == "" {
		return fmt.Errorf("backup: no database files found — has this project been used yet?")
	}

	fmt.Printf("backed up → %s\n", snapshotPath)
	return nil
}
