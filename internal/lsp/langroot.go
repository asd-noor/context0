package lsp

import (
	"os"
	"path/filepath"
)

// manifestForLang maps a language ID to the manifest file names that indicate
// a project root for that language.
var manifestForLang = map[string][]string{
	"go":         {"go.mod"},
	"python":     {"pyproject.toml", "setup.py", "setup.cfg"},
	"javascript": {"package.json", "tsconfig.json"},
	"typescript": {"package.json", "tsconfig.json"},
	"lua":        {".luarc.json"},
	"zig":        {"build.zig"},
}

// detectLangRoot searches for the language-specific project root by looking
// for the language's manifest files starting at gitRoot, using BFS up to
// maxDepth directory levels. It skips hidden directories and common
// dependency/vendor directories. Returns gitRoot if no manifest is found.
func detectLangRoot(gitRoot, langID string) string {
	manifests, ok := manifestForLang[langID]
	if !ok {
		return gitRoot
	}

	const maxDepth = 3

	// BFS queue: each entry is (dirPath, depth).
	type entry struct {
		path  string
		depth int
	}
	queue := []entry{{gitRoot, 0}}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		// Check if any manifest exists in this directory.
		for _, m := range manifests {
			if _, err := os.Stat(filepath.Join(cur.path, m)); err == nil {
				return cur.path
			}
		}

		if cur.depth >= maxDepth {
			continue
		}

		// Enqueue subdirectories, skipping vendor/hidden/node_modules.
		entries, err := os.ReadDir(cur.path)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			if name == "vendor" || name == "node_modules" || name == ".git" || len(name) > 0 && name[0] == '.' {
				continue
			}
			queue = append(queue, entry{filepath.Join(cur.path, name), cur.depth + 1})
		}
	}

	return gitRoot
}
