// Package codemapserver — resources.go
// Registers the codemap://usage-guidelines MCP resource.
package codemapserver

import (
	"context"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const usageGuidelines = `# Codemap Usage Guidelines

The codemap engine provides a live semantic graph of the codebase.

## Available Tools

| Tool                  | Purpose                                              |
|-----------------------|------------------------------------------------------|
| index                 | Trigger a full workspace scan (run once on startup)  |
| index_status          | Check whether the index is ready                     |
| get_symbols_in_file   | List all named symbols in a specific file            |
| get_symbol            | Find where a symbol is defined across all files      |
| find_impact           | Discover all callers / dependents of a symbol        |

## Recommended Workflow

1. Call index_status — if status is "idle" or "failed", call index first.
2. Wait for status to be "ready" before issuing queries.
3. Use get_symbols_in_file to explore file contents.
4. Use get_symbol to locate a specific function, type, or class.
5. Use find_impact before refactoring to understand the blast radius.

## Notes

- The index updates automatically when source files change (fsnotify).
- All query tools block internally until the index is ready (30 s max).
- Edges represent: calls, implements, references, imports.
`

// RegisterResources registers codemap MCP resources on s.
func RegisterResources(s *server.MCPServer) {
	s.AddResource(
		mcpgo.NewResource(
			"codemap://usage-guidelines",
			"Codemap usage guidelines for AI agents",
			mcpgo.WithResourceDescription("System documentation for the codemap MCP tools"),
			mcpgo.WithMIMEType("text/markdown"),
		),
		func(ctx context.Context, req mcpgo.ReadResourceRequest) ([]mcpgo.ResourceContents, error) {
			return []mcpgo.ResourceContents{
				mcpgo.TextResourceContents{
					URI:      "codemap://usage-guidelines",
					MIMEType: "text/markdown",
					Text:     usageGuidelines,
				},
			}, nil
		},
	)
}
