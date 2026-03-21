// Package codemapserver — prompts.go
// Registers MCP prompts for guided codemap workflows.
package codemapserver

import (
	"context"
	"fmt"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterPrompts registers codemap MCP prompts on s.
func RegisterPrompts(s *server.MCPServer) {
	registerExploreCodebase(s)
	registerAnalyzeChangeImpact(s)
}

func registerExploreCodebase(s *server.MCPServer) {
	s.AddPrompt(
		mcpgo.NewPrompt(
			"explore_codebase",
			mcpgo.WithPromptDescription("Guided workflow: ensure the index is ready, then explore symbols file by file."),
		),
		func(ctx context.Context, req mcpgo.GetPromptRequest) (*mcpgo.GetPromptResult, error) {
			return &mcpgo.GetPromptResult{
				Description: "Guided codebase exploration workflow",
				Messages: []mcpgo.PromptMessage{
					{
						Role: mcpgo.RoleUser,
						Content: mcpgo.TextContent{
							Type: "text",
							Text: `Follow these steps to explore the codebase:

1. Call index_status to check if the index is ready.
   - If status is "idle" or "failed", call index first and wait for it to complete.
2. Identify the files you want to explore (e.g. from a directory listing or user context).
3. For each relevant file, call get_symbols_in_file to list all named symbols.
4. For any symbol you want to understand better, call get_symbol to find its definition location.
5. To understand how a symbol is used, call find_impact to discover all callers and dependents.

Always wait for the index to be "ready" before querying symbols.`,
						},
					},
				},
			}, nil
		},
	)
}

func registerAnalyzeChangeImpact(s *server.MCPServer) {
	s.AddPrompt(
		mcpgo.NewPrompt(
			"analyze_change_impact",
			mcpgo.WithPromptDescription("Workflow: locate a symbol, find all dependents, and summarize the risk of changing it."),
			mcpgo.WithArgument("symbol_name",
				mcpgo.RequiredArgument(),
				mcpgo.ArgumentDescription("The symbol you are planning to change"),
			),
		),
		func(ctx context.Context, req mcpgo.GetPromptRequest) (*mcpgo.GetPromptResult, error) {
			symbolName := req.Params.Arguments["symbol_name"]
			if symbolName == "" {
				symbolName = "<symbol_name>"
			}
			return &mcpgo.GetPromptResult{
				Description: "Change impact analysis workflow",
				Messages: []mcpgo.PromptMessage{
					{
						Role: mcpgo.RoleUser,
						Content: mcpgo.TextContent{
							Type: "text",
							Text: fmt.Sprintf(`Analyse the impact of changing the symbol %q:

1. Call index_status — ensure status is "ready". If not, call index first.
2. Call get_symbol with symbol_name=%q to find its current definition(s).
3. Call find_impact with symbol_name=%q to retrieve all transitive dependents.
4. Review the list of impacted symbols: note their files, kinds, and line ranges.
5. Summarise:
   - How many symbols are directly affected?
   - Which files need to be updated?
   - Are there any cross-package dependencies?
   - What is the estimated risk level (low / medium / high)?

Provide your assessment before making any changes.`, symbolName, symbolName, symbolName),
						},
					},
				},
			}, nil
		},
	)
}
