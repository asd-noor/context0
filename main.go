package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	cmdagenda "context0/cmd/agenda"
	cmdask "context0/cmd/ask"
	cmdcodemap "context0/cmd/codemap"
	cmdexec "context0/cmd/exec"
	cmdmemory "context0/cmd/memory"
	"context0/internal/sidecar"
)

// Version is set at build time via -ldflags "-X main.Version=<tag>".
// It defaults to "dev" for local builds.
var Version = "dev"

func main() {
	var startDaemon bool
	var killDaemon bool

	root := &cobra.Command{
		Use:     "context0",
		Version: Version,
		Short:   "Context0 — AI-agent knowledge retrieval and task management",
		Long: `context0 is a CLI tool for AI coding agents.

It provides:
  - Memory Engine: persistent project knowledge with hybrid search
  - Agenda Engine: structured task / plan management
  - Code Exploration: symbol graph with impact analysis
  - Python Sidecar: embedding, inference, ask, exec, and discover`,

		// RunE fires when context0 is invoked with no subcommand (only flags).
		RunE: func(cmd *cobra.Command, args []string) error {
			if startDaemon {
				err := sidecar.Start()
				if errors.Is(err, sidecar.ErrAlreadyRunning) {
					fmt.Fprintln(os.Stderr, "sidecar already running")
					return nil
				}
				if err != nil {
					return fmt.Errorf("--daemon: %w", err)
				}
				fmt.Fprintln(os.Stderr, "sidecar started")
				return nil
			}

			if killDaemon {
				err := sidecar.Stop()
				if errors.Is(err, sidecar.ErrNotRunning) {
					fmt.Fprintln(os.Stderr, "sidecar not running")
					return nil
				}
				if err != nil {
					return fmt.Errorf("--kill-daemon: %w", err)
				}
				fmt.Fprintln(os.Stderr, "sidecar stopped")
				return nil
			}

			// No flags — show help.
			return cmd.Help()
		},
	}

	root.Flags().BoolVar(&startDaemon, "daemon", false, "Start the Python sidecar in the background (idempotent)")
	root.Flags().BoolVar(&killDaemon, "kill-daemon", false, "Stop the running Python sidecar")

	cwd, _ := os.Getwd()
	var projectDir string
	root.PersistentFlags().StringVarP(&projectDir, "project", "p", cwd, "Project directory (defaults to current working directory)")

	root.AddCommand(
		cmdmemory.NewCmd(&projectDir),
		cmdagenda.NewCmd(&projectDir),
		cmdcodemap.NewCmd(&projectDir),
		cmdexec.NewCmd(&projectDir),
		cmdask.NewCmd(&projectDir),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
