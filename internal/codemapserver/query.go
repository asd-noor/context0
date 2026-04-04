// Package codemapserver — query.go
// Shared query helpers used by both CLI commands and MCP tools.
package codemapserver

import (
	"context"
	"os"
	"path/filepath"

	"context0/internal/graph"
)

// NodeWithSource wraps a graph.Node with an optional source snippet.
type NodeWithSource struct {
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

// NodeToWithSource converts a graph.Node and optionally attaches its source lines.
func NodeToWithSource(n graph.Node, withSource bool) NodeWithSource {
	nws := NodeWithSource{
		ID:        n.ID,
		Name:      n.Name,
		Kind:      n.Kind,
		FilePath:  n.FilePath,
		LineStart: n.LineStart,
		LineEnd:   n.LineEnd,
		ColStart:  n.ColStart,
		ColEnd:    n.ColEnd,
		SymbolURI: n.SymbolURI,
	}
	if withSource {
		nws.Source = ReadLines(n.FilePath, n.LineStart, n.LineEnd)
	}
	return nws
}

// GetSymbolWithSource looks up all locations of a symbol and optionally
// attaches source snippets. It waits for the index to be ready first.
func (srv *Server) GetSymbolWithSource(ctx context.Context, name string, withSource bool) ([]NodeWithSource, error) {
	if err := srv.WaitForIndex(ctx); err != nil {
		return nil, err
	}
	nodes, err := srv.store.GetSymbolLocation(ctx, name, "")
	if err != nil {
		return nil, err
	}
	results := make([]NodeWithSource, len(nodes))
	for i, n := range nodes {
		results[i] = NodeToWithSource(n, withSource)
	}
	return results, nil
}

// AbsFilePath resolves a path to absolute, using cwd as the base for relative paths.
func AbsFilePath(path string) (string, error) {
	if filepath.IsAbs(path) {
		return path, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, path), nil
}

// ReadLines reads lines [start, end] (1-indexed, inclusive) from path.
func ReadLines(path string, start, end int) string {
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

// splitLines splits byte data into string lines.
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
