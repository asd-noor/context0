// Package cmdrecover implements the `context0 recover` subcommand, which
// restores per-project databases from the latest automatic backup created by
// `context0 backup`.
package cmdrecover

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"context0/internal/archive"
	"context0/internal/db"
)

// NewCmd returns the `recover` cobra.Command.
func NewCmd(projectDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "recover",
		Short: "Restore project databases from the latest automatic backup",
		Long: `Restore context0 databases from the most recent snapshot in:

  ~/.context0/backup/<encoded-project>/

Before overwriting any files the current state is snapshotted for safety.
To restore from an arbitrary archive use 'context0 import <file.tar.gz>'.`,

		Args: cobra.NoArgs,

		RunE: func(cmd *cobra.Command, args []string) error {
			return run(*projectDir)
		},
	}
}

func run(projectDir string) error {
	dataDir, err := db.ProjectDir(projectDir)
	if err != nil {
		return fmt.Errorf("recover: resolve project dir: %w", err)
	}

	backupDir, err := archive.BackupDir(dataDir)
	if err != nil {
		return fmt.Errorf("recover: %w", err)
	}

	latest, err := latestArchive(backupDir)
	if err != nil {
		return fmt.Errorf("recover: %w", err)
	}

	// Safety snapshot of current state before overwriting.
	snapshotPath, err := archive.Snapshot(dataDir)
	if err != nil {
		return fmt.Errorf("recover: snapshot: %w", err)
	}
	if snapshotPath != "" {
		fmt.Printf("snapshot → %s\n", snapshotPath)
	}

	n, err := archive.Extract(dataDir, latest)
	if err != nil {
		return fmt.Errorf("recover: %w", err)
	}

	fmt.Printf("%d file(s) restored → %s\n", n, dataDir)
	return nil
}

// latestArchive returns the path of the newest .tar.gz file in dir.
func latestArchive(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no backups found in %q — run 'context0 backup' first", dir)
		}
		return "", fmt.Errorf("read backup dir %q: %w", dir, err)
	}

	var archives []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".tar.gz") {
			archives = append(archives, e.Name())
		}
	}

	if len(archives) == 0 {
		return "", fmt.Errorf("no backups found in %q — run 'context0 backup' first", dir)
	}

	sort.Strings(archives) // timestamps sort lexicographically
	return filepath.Join(dir, archives[len(archives)-1]), nil
}
