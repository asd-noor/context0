package lsp_test

import (
	"strings"
	"testing"

	"context0/internal/lsp"
)

func TestPathToURIScheme(t *testing.T) {
	uri := lsp.PathToURI("/usr/local/src/main.go")
	if !strings.HasPrefix(uri, "file://") {
		t.Fatalf("PathToURI(%q) = %q, want file:// prefix", "/usr/local/src/main.go", uri)
	}
}

func TestPathToURIRoundTrip(t *testing.T) {
	path := "/usr/local/src/main.go"
	uri := lsp.PathToURI(path)
	got := lsp.URIToPath(uri)
	if got != path {
		t.Fatalf("round-trip: PathToURI→URIToPath: got %q, want %q", got, path)
	}
}

func TestURIToPathMalformed(t *testing.T) {
	got := lsp.URIToPath("file:///tmp/foo.go")
	if got != "/tmp/foo.go" {
		t.Fatalf("URIToPath(file:///tmp/foo.go) = %q, want /tmp/foo.go", got)
	}
}
