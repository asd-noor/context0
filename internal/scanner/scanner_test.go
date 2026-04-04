package scanner_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"context0/internal/scanner"
)

// projectRoot returns the absolute path to the context0 repository root
// (two directories up from internal/scanner).
func projectRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../")
	if err != nil {
		t.Fatalf("projectRoot: %v", err)
	}
	return root
}

// ── helpers ───────────────────────────────────────────────────────────────────

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeFile %s: %v", name, err)
	}
	return path
}

func newScanner(root string) *scanner.Scanner {
	return scanner.New(root)
}

// ── ScanFile — Go ─────────────────────────────────────────────────────────────

func TestScanFileGoFunctions(t *testing.T) {
	root := projectRoot(t)
	path := filepath.Join(root, "internal", "graph", "hash.go")
	s := newScanner(root)
	nodes, err := s.ScanFile(context.Background(), path)
	if err != nil {
		t.Fatalf("ScanFile: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d: %v", len(nodes), nodes)
	}
	names := map[string]string{}
	for _, n := range nodes {
		names[n.Name] = n.Kind
	}
	if names["NodeID"] != "function" {
		t.Errorf("expected NodeID to be function, got %q", names["NodeID"])
	}
	if names["DiagnosticID"] != "function" {
		t.Errorf("expected DiagnosticID to be function, got %q", names["DiagnosticID"])
	}
}

func TestScanFileGoMethod(t *testing.T) {
	root := projectRoot(t)
	path := filepath.Join(root, "internal", "memory", "engine.go")
	s := newScanner(root)
	nodes, err := s.ScanFile(context.Background(), path)
	if err != nil {
		t.Fatalf("ScanFile: %v", err)
	}
	kinds := map[string]string{}
	for _, n := range nodes {
		kinds[n.Name] = n.Kind
	}
	if kinds["Engine"] != "type" {
		t.Errorf("expected Engine kind=type, got %q", kinds["Engine"])
	}
	if kinds["SaveMemory"] != "method" {
		t.Errorf("expected SaveMemory kind=method, got %q", kinds["SaveMemory"])
	}
}

// ── ScanFile — Python ─────────────────────────────────────────────────────────

func TestScanFilePython(t *testing.T) {
	root := projectRoot(t)
	path := filepath.Join(root, "sidecar", "embed.py")
	s := newScanner(root)
	nodes, err := s.ScanFile(context.Background(), path)
	if err != nil {
		t.Fatalf("ScanFile: %v", err)
	}
	kinds := map[string]string{}
	for _, n := range nodes {
		kinds[n.Name] = n.Kind
	}
	if kinds["EmbedEngine"] != "class" {
		t.Errorf("expected EmbedEngine=class, got %q", kinds["EmbedEngine"])
	}
	if kinds["load"] != "function" {
		t.Errorf("expected load=function, got %q", kinds["load"])
	}
	if kinds["embed"] != "function" {
		t.Errorf("expected embed=function, got %q", kinds["embed"])
	}
}

// ── ScanFile — TypeScript ─────────────────────────────────────────────────────

func TestScanFileTypeScript(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "index.ts", `
function greet(name: string): string {
    return "hello " + name;
}

interface Greeter {
    describe(): string;
}

class ConsoleGreeter implements Greeter {
    sayHello(name: string): string { return name; }
    describe(): string { return "console"; }
}
`)
	s := newScanner(dir)
	nodes, err := s.ScanFile(context.Background(), path)
	if err != nil {
		t.Fatalf("ScanFile: %v", err)
	}
	kinds := map[string]string{}
	for _, n := range nodes {
		kinds[n.Name] = n.Kind
	}
	if kinds["greet"] != "function" {
		t.Errorf("expected greet=function, got %q", kinds["greet"])
	}
	if kinds["Greeter"] != "interface" {
		t.Errorf("expected Greeter=interface, got %q", kinds["Greeter"])
	}
	if kinds["ConsoleGreeter"] != "class" {
		t.Errorf("expected ConsoleGreeter=class, got %q", kinds["ConsoleGreeter"])
	}
	if kinds["sayHello"] != "method" {
		t.Errorf("expected sayHello=method, got %q", kinds["sayHello"])
	}
}

// ── ScanFile — JavaScript ─────────────────────────────────────────────────────

func TestScanFileJavaScript(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "app.js", `
function add(a, b) { return a + b; }

const multiply = (a, b) => a * b;

class Calculator {
    sum(a, b) { return a + b; }
}
`)
	s := newScanner(dir)
	nodes, err := s.ScanFile(context.Background(), path)
	if err != nil {
		t.Fatalf("ScanFile: %v", err)
	}
	kinds := map[string]string{}
	for _, n := range nodes {
		kinds[n.Name] = n.Kind
	}
	if kinds["add"] != "function" {
		t.Errorf("expected add=function, got %q", kinds["add"])
	}
	if kinds["multiply"] != "function" {
		t.Errorf("expected multiply=function, got %q", kinds["multiply"])
	}
	if kinds["Calculator"] != "class" {
		t.Errorf("expected Calculator=class, got %q", kinds["Calculator"])
	}
	if kinds["sum"] != "method" {
		t.Errorf("expected sum=method, got %q", kinds["sum"])
	}
}

// ── ScanFile — generated files skipped ───────────────────────────────────────

func TestScanFileSkipsGeneratedSQLGo(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "schema.sql.go", `package db
func generatedFunc() {}
`)
	s := newScanner(dir)
	nodes, err := s.ScanFile(context.Background(), path)
	if err != nil {
		t.Fatalf("ScanFile: %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("expected 0 nodes for generated file, got %d", len(nodes))
	}
}

func TestScanFileSkipsStringerGenerated(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "status_string.go", `package model
func (s Status) String() string { return "" }
`)
	s := newScanner(dir)
	nodes, err := s.ScanFile(context.Background(), path)
	if err != nil {
		t.Fatalf("ScanFile: %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("expected 0 nodes for _string.go file, got %d", len(nodes))
	}
}

// ── ScanFile — unsupported extension ─────────────────────────────────────────

func TestScanFileUnsupportedExtension(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "config.yaml", `key: value`)
	s := newScanner(dir)
	nodes, err := s.ScanFile(context.Background(), path)
	if err != nil {
		t.Fatalf("ScanFile: %v", err)
	}
	if nodes != nil {
		t.Fatalf("expected nil for unsupported extension, got %v", nodes)
	}
}

// ── ScanFile — node fields ────────────────────────────────────────────────────

func TestScanFileNodeFields(t *testing.T) {
	root := projectRoot(t)
	path := filepath.Join(root, "internal", "graph", "hash.go")
	s := newScanner(root)
	nodes, err := s.ScanFile(context.Background(), path)
	if err != nil {
		t.Fatalf("ScanFile: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("expected at least one node")
	}
	n := nodes[0]
	if n.ID == "" {
		t.Error("node ID is empty")
	}
	if n.FilePath == "" {
		t.Error("node FilePath is empty")
	}
	if n.LineStart <= 0 {
		t.Errorf("node LineStart = %d, want > 0", n.LineStart)
	}
	if n.LineEnd < n.LineStart {
		t.Errorf("node LineEnd %d < LineStart %d", n.LineEnd, n.LineStart)
	}
	if n.NameLine <= 0 {
		t.Errorf("node NameLine = %d, want > 0", n.NameLine)
	}
}

// ── Scan — directory walk ─────────────────────────────────────────────────────

func TestScanWalkFindsMultipleFiles(t *testing.T) {
	root := projectRoot(t)
	s := newScanner(root)
	nodes, err := s.Scan(context.Background(), root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(nodes) < 2 {
		t.Fatalf("expected at least 2 nodes, got %d", len(nodes))
	}
	// Verify at least one .go symbol and one .py symbol are present.
	var hasGo, hasPy bool
	for _, n := range nodes {
		if strings.HasSuffix(n.FilePath, ".go") {
			hasGo = true
		}
		if strings.HasSuffix(n.FilePath, ".py") {
			hasPy = true
		}
	}
	if !hasGo {
		t.Error("expected at least one symbol from a .go file")
	}
	if !hasPy {
		t.Error("expected at least one symbol from a .py file")
	}
}

func TestScanSkipsVendorDir(t *testing.T) {
	dir := t.TempDir()
	vendorDir := filepath.Join(dir, "vendor")
	if err := os.Mkdir(vendorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, vendorDir, "external.go", `package ext
func External() {}
`)
	writeFile(t, dir, "main.go", `package main
func main() {}
`)
	s := newScanner(dir)
	nodes, err := s.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for _, n := range nodes {
		if filepath.Dir(n.FilePath) == vendorDir {
			t.Errorf("vendor dir was not skipped: found node %q from %q", n.Name, n.FilePath)
		}
	}
}

func TestScanSkipsHiddenDir(t *testing.T) {
	dir := t.TempDir()
	hiddenDir := filepath.Join(dir, ".hidden")
	if err := os.Mkdir(hiddenDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, hiddenDir, "secret.go", `package secret
func Secret() {}
`)
	writeFile(t, dir, "visible.go", `package main
func Visible() {}
`)
	s := newScanner(dir)
	nodes, err := s.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for _, n := range nodes {
		if filepath.Dir(n.FilePath) == hiddenDir {
			t.Errorf("hidden dir was not skipped: found %q in %q", n.Name, n.FilePath)
		}
	}
}

func TestScanRespectsGitignore(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".gitignore", "ignored.go\n")
	writeFile(t, dir, "ignored.go", `package p
func Ignored() {}
`)
	writeFile(t, dir, "kept.go", `package p
func Kept() {}
`)
	s := newScanner(dir)
	nodes, err := s.Scan(context.Background(), dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for _, n := range nodes {
		if n.Name == "Ignored" {
			t.Errorf("gitignored file was scanned: found symbol %q", n.Name)
		}
	}
	found := false
	for _, n := range nodes {
		if n.Name == "Kept" {
			found = true
		}
	}
	if !found {
		t.Error("expected Kept to be in scan results")
	}
}

func TestScanContextCancellation(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 5; i++ {
		writeFile(t, dir, "f"+string(rune('0'+i))+".go",
			"package p\nfunc F() {}\n")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	s := newScanner(dir)
	_, err := s.Scan(ctx, dir)
	// Should return context error or empty — must not panic.
	_ = err
}
