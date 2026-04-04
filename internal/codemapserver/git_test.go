package codemapserver_test

import (
	"os"
	"path/filepath"
	"testing"

	"context0/internal/codemapserver"
)

func TestFindGitRootFindsRoot(t *testing.T) {
	// The context0 repo itself has a .git at the workspace root.
	// Walk up from a deep subdir and confirm we land on the repo root.
	sub := filepath.Join("..", "internal", "graph")
	root := codemapserver.FindGitRoot(sub)

	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		t.Fatalf("FindGitRoot(%q) = %q — no .git found there: %v", sub, root, err)
	}
}

func TestFindGitRootNoGit(t *testing.T) {
	dir := t.TempDir()
	got := codemapserver.FindGitRoot(dir)
	if !filepath.IsAbs(got) {
		t.Fatalf("FindGitRoot returned non-absolute path: %q", got)
	}
}
