// Package codemapserver — tools.go
// Registers MCP tools for the codemap engine.
package codemapserver

import (
	"context"
	"encoding/json"
	"fmt"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterTools registers all codemap MCP tools on s.
func RegisterTools(s *server.MCPServer, srv *Server) {
	registerIndex(s, srv)
	registerIndexStatus(s, srv)
	registerGetSymbolsInFile(s, srv)
	registerGetSymbol(s, srv)
	registerFindImpact(s, srv)
}

// --- index ---

func registerIndex(s *server.MCPServer, srv *Server) {
	s.AddTool(mcpgo.NewTool("index",
		mcpgo.WithDescription("Scan workspace and build/rebuild the symbol graph. Returns node and edge counts."),
		mcpgo.WithBoolean("force", mcpgo.Description("Force a full re-index even if one is in progress")),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		if err := srv.ForceIndex(ctx); err != nil {
			return toolError(err), nil
		}
		// Wait for the index to complete.
		if err := srv.WaitForIndex(ctx); err != nil {
			return toolError(err), nil
		}
		nodeCount, err := srv.store.NodeCount(ctx)
		if err != nil {
			return toolError(err), nil
		}
		edgeCount, err := srv.store.EdgeCount(ctx)
		if err != nil {
			return toolError(err), nil
		}
		return toolJSON(map[string]any{
			"status":     "ready",
			"node_count": nodeCount,
			"edge_count": edgeCount,
		}), nil
	})
}

// --- index_status ---

func registerIndexStatus(s *server.MCPServer, srv *Server) {
	s.AddTool(mcpgo.NewTool("index_status",
		mcpgo.WithDescription("Return the current codemap index status and duration."),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		status, dur, indexErr := srv.Status()
		result := map[string]any{
			"status":   status.String(),
			"duration": dur.String(),
		}
		if indexErr != nil {
			result["error"] = indexErr.Error()
		}
		return toolJSON(result), nil
	})
}

// --- get_symbols_in_file ---

func registerGetSymbolsInFile(s *server.MCPServer, srv *Server) {
	s.AddTool(mcpgo.NewTool("get_symbols_in_file",
		mcpgo.WithDescription("List all symbols in a file with their name, kind, and line range."),
		mcpgo.WithString("file_path", mcpgo.Description("Absolute or project-relative file path"), mcpgo.Required()),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		if err := srv.WaitForIndex(ctx); err != nil {
			return toolError(err), nil
		}
		filePath := req.GetString("file_path", "")
		if filePath == "" {
			return toolError(fmt.Errorf("file_path is required")), nil
		}
		// Resolve to absolute path.
		absPath, err := AbsFilePath(filePath)
		if err != nil {
			return toolError(err), nil
		}
		nodes, err := srv.store.GetSymbolsInFile(ctx, absPath)
		if err != nil {
			return toolError(err), nil
		}
		return toolJSON(nodes), nil
	})
}

// --- get_symbol ---

func registerGetSymbol(s *server.MCPServer, srv *Server) {
	s.AddTool(mcpgo.NewTool("get_symbol",
		mcpgo.WithDescription("Find all locations of a symbol by name. Optionally include source code snippet."),
		mcpgo.WithString("symbol_name", mcpgo.Description("Symbol name to look up"), mcpgo.Required()),
		mcpgo.WithBoolean("with_source", mcpgo.Description("Include the source code lines for each match")),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		name := req.GetString("symbol_name", "")
		if name == "" {
			return toolError(fmt.Errorf("symbol_name is required")), nil
		}
		withSource := false
		if v, ok := req.GetArguments()["with_source"]; ok && v != nil {
			withSource, _ = v.(bool)
		}
		results, err := srv.GetSymbolWithSource(ctx, name, withSource)
		if err != nil {
			return toolError(err), nil
		}
		return toolJSON(results), nil
	})
}

// --- find_impact ---

func registerFindImpact(s *server.MCPServer, srv *Server) {
	s.AddTool(mcpgo.NewTool("find_impact",
		mcpgo.WithDescription("Find all symbols that transitively depend on the given symbol (change impact analysis)."),
		mcpgo.WithString("symbol_name", mcpgo.Description("Symbol name to analyse"), mcpgo.Required()),
	), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		if err := srv.WaitForIndex(ctx); err != nil {
			return toolError(err), nil
		}
		name := req.GetString("symbol_name", "")
		if name == "" {
			return toolError(fmt.Errorf("symbol_name is required")), nil
		}
		nodes, err := srv.store.FindImpact(ctx, name)
		if err != nil {
			return toolError(err), nil
		}
		return toolJSON(nodes), nil
	})
}

// --- helpers ---

func toolError(err error) *mcpgo.CallToolResult {
	return mcpgo.NewToolResultError(err.Error())
}

func toolJSON(v any) *mcpgo.CallToolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return toolError(err)
	}
	return mcpgo.NewToolResultText(string(b))
}
