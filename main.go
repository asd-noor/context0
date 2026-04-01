package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	cmdagenda "context0/cmd/agenda"
	cmdcodemap "context0/cmd/codemap"
	cmdmemory "context0/cmd/memory"
)

// Version is set at build time via -ldflags "-X main.Version=<tag>".
// It defaults to "dev" for local builds.
var Version = "dev"

func main() {
	root := &cobra.Command{
		Use:     "context0",
		Version: Version,
		Short:   "Context0 — AI-agent knowledge retrieval and task management",
		Long: `context0 is a CLI tool for AI coding agents.

It provides:
  - Memory Engine: persistent project knowledge with hybrid search
  - Agenda Engine: structured task / plan management
  - Code Exploration: symbol graph with impact analysis`,
	}

	cwd, _ := os.Getwd()
	var projectDir string
	root.PersistentFlags().StringVarP(&projectDir, "project", "p", cwd, "Project directory (defaults to current working directory)")

	root.AddCommand(
		cmdmemory.NewCmd(&projectDir),
		cmdagenda.NewCmd(&projectDir),
		cmdcodemap.NewCmd(&projectDir),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
