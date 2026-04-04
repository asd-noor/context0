// Package scanner walks a project directory, parses source files with
// Tree-sitter, and extracts named symbol nodes for the codemap graph.
package scanner

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	sitter "github.com/tree-sitter/go-tree-sitter"

	tree_sitter_lua "github.com/tree-sitter-grammars/tree-sitter-lua/bindings/go"
	tree_sitter_zig "github.com/tree-sitter-grammars/tree-sitter-zig/bindings/go"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tree_sitter_javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"

	gitignore "github.com/sabhiram/go-gitignore"

	"context0/internal/graph"
)

// langDef binds a Tree-sitter language grammar to its query and LSP language ID.
type langDef struct {
	lang   *sitter.Language
	query  string
	langID string // LSP language identifier (e.g. "go", "python")
}

// extToLang maps file extensions to language definitions.
var extToLang = map[string]langDef{
	".go":  {lang: sitter.NewLanguage(tree_sitter_go.Language()), query: queries["go"], langID: "go"},
	".py":  {lang: sitter.NewLanguage(tree_sitter_python.Language()), query: queries["python"], langID: "python"},
	".js":  {lang: sitter.NewLanguage(tree_sitter_javascript.Language()), query: queries["javascript"], langID: "javascript"},
	".jsx": {lang: sitter.NewLanguage(tree_sitter_javascript.Language()), query: queries["javascript"], langID: "javascript"},
	".ts":  {lang: sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript()), query: queries["typescript"], langID: "typescript"},
	".tsx": {lang: sitter.NewLanguage(tree_sitter_typescript.LanguageTSX()), query: queries["typescript"], langID: "typescript"},
	".lua": {lang: sitter.NewLanguage(tree_sitter_lua.Language()), query: queries["lua"], langID: "lua"},
	".zig": {lang: sitter.NewLanguage(tree_sitter_zig.Language()), query: queries["zig"], langID: "zig"},
}

// generatedSuffixes lists file-name suffixes that indicate machine-generated code.
var generatedSuffixes = []string{
	".sql.go",
	"_string.go",
}

// skipDirs is the set of directory names always skipped.
var skipDirs = map[string]struct{}{
	"vendor":       {},
	"node_modules": {},
	"__pycache__":  {},
	".git":         {},
	"zig-cache":    {},
	"zig-out":      {},
}

// Scanner walks a directory tree and extracts graph nodes via Tree-sitter.
type Scanner struct {
	ignore *gitignore.GitIgnore
}

// New creates a Scanner for the given root directory, loading .gitignore if
// present.
func New(root string) *Scanner {
	ig, _ := gitignore.CompileIgnoreFile(filepath.Join(root, ".gitignore"))
	return &Scanner{ignore: ig}
}

// Scan walks root recursively, returning all discovered nodes.
// Files are parsed concurrently using a worker pool sized to runtime.NumCPU().
func (s *Scanner) Scan(ctx context.Context, root string) ([]graph.Node, error) {
	// Phase 1: sequential walk to collect candidate file paths.
	// WalkDir itself cannot be parallelised (it must respect SkipDir returns).
	var paths []string
	if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		name := d.Name()
		if d.IsDir() {
			if strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			if _, ok := skipDirs[name]; ok {
				return filepath.SkipDir
			}
			return nil
		}
		// Skip gitignore-matched paths.
		if s.ignore != nil {
			rel, _ := filepath.Rel(root, path)
			if s.ignore.MatchesPath(rel) {
				return nil
			}
		}
		// Only enqueue files with a recognised extension.
		if _, ok := extToLang[strings.ToLower(filepath.Ext(path))]; ok {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	if len(paths) == 0 {
		return nil, nil
	}

	// Phase 2: parse files concurrently.
	// Each ScanFile call creates its own sitter.Parser, so there is no shared
	// mutable state — the workers are fully independent.
	numWorkers := runtime.NumCPU()
	if numWorkers > len(paths) {
		numWorkers = len(paths)
	}

	pathCh := make(chan string, numWorkers)
	resultCh := make(chan []graph.Node, numWorkers)

	// Producer: feed paths into pathCh, then close it.
	go func() {
		for _, p := range paths {
			select {
			case <-ctx.Done():
				break
			case pathCh <- p:
			}
		}
		close(pathCh)
	}()

	// Workers: drain pathCh, parse each file, send results.
	var wg sync.WaitGroup
	for range numWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range pathCh {
				fileNodes, _ := s.ScanFile(ctx, p) // skip unparseable files
				if len(fileNodes) > 0 {
					resultCh <- fileNodes
				}
			}
		}()
	}

	// Close resultCh once all workers finish.
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// Collector: gather results in the calling goroutine.
	var nodes []graph.Node
	for fileNodes := range resultCh {
		nodes = append(nodes, fileNodes...)
	}

	return nodes, ctx.Err()
}

// DetectLanguages walks dir and returns the unique set of LSP language IDs for
// which source files exist. It applies the same directory-skip and gitignore
// rules as Scan but does no parsing — only file extensions are checked.
func (s *Scanner) DetectLanguages(dir string) []string {
	seen := make(map[string]struct{})
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error { //nolint:errcheck
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			if _, ok := skipDirs[name]; ok {
				return filepath.SkipDir
			}
			return nil
		}
		if s.ignore != nil {
			rel, _ := filepath.Rel(dir, path)
			if s.ignore.MatchesPath(rel) {
				return nil
			}
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ld, ok := extToLang[ext]; ok {
			seen[ld.langID] = struct{}{}
		}
		return nil
	})
	langs := make([]string, 0, len(seen))
	for id := range seen {
		langs = append(langs, id)
	}
	return langs
}

// ScanFile parses a single file and returns its symbol nodes.
// Returns nil, nil for unsupported or generated files.
func (s *Scanner) ScanFile(ctx context.Context, path string) ([]graph.Node, error) {
	ext := strings.ToLower(filepath.Ext(path))
	ld, ok := extToLang[ext]
	if !ok {
		return nil, nil
	}
	if isGenerated(path) {
		return nil, nil
	}

	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(ld.lang); err != nil {
		return nil, err
	}

	tree := parser.Parse(src, nil)
	if tree == nil {
		return nil, nil
	}
	defer tree.Close()

	q, qErr := sitter.NewQuery(ld.lang, ld.query)
	if qErr != nil {
		return nil, fmt.Errorf("tree-sitter query: %s", qErr.Message)
	}
	defer q.Close()

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()
	matches := cursor.Matches(q, tree.RootNode(), src)

	absPath, _ := filepath.Abs(path)
	captureNames := q.CaptureNames()

	var nodes []graph.Node
	for {
		match := matches.Next()
		if match == nil {
			break
		}

		var nameNode, defNode *sitter.Node
		var defKind string

		for _, cap := range match.Captures {
			capName := captureNames[cap.Index]
			node := cap.Node // value copy
			if capName == "name" {
				nameNode = &node
			} else if strings.HasPrefix(capName, "definition.") {
				defNode = &node
				defKind = strings.TrimPrefix(capName, "definition.")
			}
		}

		if nameNode == nil || defNode == nil {
			continue
		}

		symbolName := nameNode.Utf8Text(src)
		if symbolName == "" {
			continue
		}

		start := defNode.StartPosition()
		end := defNode.EndPosition()
		nameStart := nameNode.StartPosition()

		n := graph.Node{
			ID:        graph.NodeID(absPath, symbolName, defKind),
			Name:      symbolName,
			Kind:      defKind,
			FilePath:  absPath,
			LineStart: int(start.Row) + 1, // Tree-sitter is 0-indexed
			LineEnd:   int(end.Row) + 1,
			ColStart:  int(start.Column) + 1,
			ColEnd:    int(end.Column) + 1,
			NameLine:  int(nameStart.Row) + 1,
			NameCol:   int(nameStart.Column) + 1,
		}
		nodes = append(nodes, n)
	}

	return nodes, nil
}

// isGenerated reports whether the file name matches a generated-code suffix.
func isGenerated(path string) bool {
	base := filepath.Base(path)
	for _, suf := range generatedSuffixes {
		if strings.HasSuffix(base, suf) {
			return true
		}
	}
	return false
}
