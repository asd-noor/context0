// Package codemap provides the `ctx0 codemap mcp` command, which starts a
// stdio MCP server that exposes the Code Exploration Engine (codemap) tools.
package codemap

import (
	"context"
	"fmt"
	"os"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"

	"context0/internal/codemapserver"
)

// NewCmd returns the `codemap` sub-command.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "codemap",
		Short: "Code Exploration Engine — codemap sub-commands",
	}
	cmd.AddCommand(newMCPCmd())
	return cmd
}

// newMCPCmd returns the `codemap mcp` sub-command that starts the MCP stdio server.
func newMCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Start the codemap MCP stdio server (index, get_symbols_in_file, find_impact, …)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCodemapMCP()
		},
	}
}

func projectRoot() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

func runCodemapMCP() error {
	ctx := context.Background()

	srv, err := codemapserver.New(ctx, projectRoot())
	if err != nil {
		return fmt.Errorf("codemap mcp: start server: %w", err)
	}
	defer srv.Close()

	s := server.NewMCPServer(
		"context0-codemap",
		"0.1.0",
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(true, false),
		server.WithPromptCapabilities(true),
	)

	codemapserver.RegisterTools(s, srv)
	codemapserver.RegisterResources(s)
	codemapserver.RegisterPrompts(s)

	_ = mcpgo.NewTool // ensure mcp package is used (compiler keeps import live)

	return server.ServeStdio(s)
}
