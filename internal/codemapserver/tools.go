// Package codemapserver — tools.go
// Registers MCP tools for the codemap engine.
package codemapserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

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
		absPath, err := absFilePath(filePath)
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
		if err := srv.WaitForIndex(ctx); err != nil {
			return toolError(err), nil
		}
		name := req.GetString("symbol_name", "")
		if name == "" {
			return toolError(fmt.Errorf("symbol_name is required")), nil
		}
		withSource := false
		if v, ok := req.GetArguments()["with_source"]; ok && v != nil {
			withSource, _ = v.(bool)
		}

		nodes, err := srv.store.GetSymbolLocation(ctx, name)
		if err != nil {
			return toolError(err), nil
		}

		if !withSource {
			return toolJSON(nodes), nil
		}

		// Attach source snippets.
		type nodeWithSource struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Kind      string `json:"kind"`
			FilePath  string `json:"file_path"`
			LineStart int    `json:"line_start"`
			LineEnd   int    `json:"line_end"`
			ColStart  int    `json:"col_start"`
			ColEnd    int    `json:"col_end"`
			SymbolURI string `json:"symbol_uri,omitempty"`
			Source    string `json:"source,omitempty"`
		}
		var results []nodeWithSource
		for _, n := range nodes {
			nws := nodeWithSource{
				ID: n.ID, Name: n.Name, Kind: n.Kind,
				FilePath: n.FilePath, LineStart: n.LineStart, LineEnd: n.LineEnd,
				ColStart: n.ColStart, ColEnd: n.ColEnd, SymbolURI: n.SymbolURI,
			}
			nws.Source = readLines(n.FilePath, n.LineStart, n.LineEnd)
			results = append(results, nws)
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

// absFilePath resolves path to an absolute path, using cwd as the base.
func absFilePath(path string) (string, error) {
	if len(path) > 0 && path[0] == '/' {
		return path, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return cwd + "/" + path, nil
}

// readLines reads lines [start, end] (1-indexed) from path.
func readLines(path string, start, end int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := splitLines(data)
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	if start > end {
		return ""
	}
	result := ""
	for i := start - 1; i < end; i++ {
		result += lines[i] + "\n"
	}
	return result
}

// splitLines splits data into lines without allocating a full strings.Split.
func splitLines(data []byte) []string {
	var lines []string
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, string(data[start:i]))
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, string(data[start:]))
	}
	return lines
}
