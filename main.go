package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	cmdagenda "context0/cmd/agenda"
	cmdcodemap "context0/cmd/codemap"
	cmdmcp "context0/cmd/mcp"
	cmdmemory "context0/cmd/memory"
)

func main() {
	root := &cobra.Command{
		Use:   "context0",
		Short: "Context0 — AI-agent knowledge retrieval and task management",
		Long: `context0 is a CLI tool and MCP daemon for AI coding agents.

It provides:
  - Memory Engine: persistent project knowledge with hybrid search
  - Agenda Engine: structured task / plan management
  - MCP server:    expose all tools via Model Context Protocol (stdio)`,
	}

	cwd, _ := os.Getwd()
	var projectDir string
	root.PersistentFlags().StringVarP(&projectDir, "project", "p", cwd, "Project directory (defaults to current working directory)")

	root.AddCommand(
		cmdmemory.NewCmd(&projectDir),
		cmdagenda.NewCmd(&projectDir),
		cmdmcp.NewCmd(&projectDir),
		cmdcodemap.NewCmd(&projectDir),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
