// Package scanner walks a project directory, parses source files with
// Tree-sitter, and extracts named symbol nodes for the codemap graph.
package scanner

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/lua"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/typescript/typescript"

	gitignore "github.com/sabhiram/go-gitignore"

	"context0/internal/graph"
	"context0/util"
)

// langDef binds a Tree-sitter language grammar to its query.
type langDef struct {
	lang  *sitter.Language
	query string
}

// extToLang maps file extensions to language definitions.
var extToLang = map[string]langDef{
	".go":  {lang: golang.GetLanguage(), query: queries["go"]},
	".py":  {lang: python.GetLanguage(), query: queries["python"]},
	".js":  {lang: javascript.GetLanguage(), query: queries["javascript"]},
	".jsx": {lang: javascript.GetLanguage(), query: queries["javascript"]},
	".ts":  {lang: typescript.GetLanguage(), query: queries["typescript"]},
	".tsx": {lang: typescript.GetLanguage(), query: queries["typescript"]},
	".lua": {lang: lua.GetLanguage(), query: queries["lua"]},
}

// generatedSuffixes lists file-name suffixes that indicate machine-generated code.
var generatedSuffixes = []string{
	"_templ.go",
	".sql.go",
	"_string.go",
}

// skipDirs is the set of directory names always skipped.
var skipDirs = map[string]struct{}{
	"vendor":       {},
	"node_modules": {},
	"__pycache__":  {},
	".git":         {},
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
func (s *Scanner) Scan(ctx context.Context, root string) ([]graph.Node, error) {
	var nodes []graph.Node

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		name := d.Name()

		// Skip hidden directories and well-known skip dirs.
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

		fileNodes, err := s.ScanFile(ctx, path)
		if err != nil {
			return nil // skip unparseable files
		}
		nodes = append(nodes, fileNodes...)
		return nil
	})
	return nodes, err
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
	parser.SetLanguage(ld.lang)

	tree, err := parser.ParseCtx(ctx, nil, src)
	if err != nil {
		return nil, err
	}
	defer tree.Close()

	q, err := sitter.NewQuery([]byte(ld.query), ld.lang)
	if err != nil {
		return nil, err
	}
	defer q.Close()

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()
	cursor.Exec(q, tree.RootNode())

	absPath, _ := filepath.Abs(path)

	var nodes []graph.Node
	for {
		match, ok := cursor.NextMatch()
		if !ok {
			break
		}

		var nameNode, defNode *sitter.Node
		var defKind string

		for _, cap := range match.Captures {
			capName := q.CaptureNameForId(cap.Index)
			if capName == "name" {
				nameNode = cap.Node
			} else if strings.HasPrefix(capName, "definition.") {
				defNode = cap.Node
				defKind = strings.TrimPrefix(capName, "definition.")
			}
		}

		if nameNode == nil || defNode == nil {
			continue
		}

		symbolName := nameNode.Content(src)
		if symbolName == "" {
			continue
		}

		start := defNode.StartPoint()
		end := defNode.EndPoint()

		n := graph.Node{
			ID:        util.NodeID(absPath, symbolName, defKind),
			Name:      symbolName,
			Kind:      defKind,
			FilePath:  absPath,
			LineStart: int(start.Row) + 1, // Tree-sitter is 0-indexed
			LineEnd:   int(end.Row) + 1,
			ColStart:  int(start.Column) + 1,
			ColEnd:    int(end.Column) + 1,
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
