// Package lsp implements an LSP client used by the codemap engine to enrich
// the symbol graph with cross-file reference and implementation edges.
//
// Each language server gets one persistent Client subprocess. Enrichment runs
// in a pool of 10 goroutines.
package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"context0/internal/graph"
	"context0/internal/pkgmgr"
	"context0/internal/scanner"
)

const (
	workerCount    = 10
	callTimeout    = 10 * time.Second
	warmupDuration = 5 * time.Second
)

// langServer describes one language server binary.
type langServer struct {
	langID string
	binary string
	args   []string
}

// languageServers lists all supported language servers.
var languageServers = []langServer{
	{langID: "go", binary: "gopls", args: []string{"serve"}},
	{langID: "python", binary: "pyright-langserver", args: []string{"--stdio"}},
	{langID: "javascript", binary: "typescript-language-server", args: []string{"--stdio"}},
	{langID: "typescript", binary: "typescript-language-server", args: []string{"--stdio"}},
	{langID: "lua", binary: "lua-language-server", args: []string{"--stdio"}},
	{langID: "zig", binary: "zls", args: nil},
}

// serverArgs returns the effective command-line arguments for spec, injecting
// any langRoot-dependent flags. Currently this adds --chdir=<langRoot> for
// lua-language-server so it anchors its workspace and .luarc.json discovery to
// the language-specific root rather than the process CWD.
func serverArgs(spec *langServer, langRoot string) []string {
	if spec.langID == "lua" {
		args := make([]string, len(spec.args))
		copy(args, spec.args)
		return append(args, "--chdir="+langRoot)
	}
	return spec.args
}

// incomingMessage is used internally to parse any LSP message from the server.
// It covers both JSON-RPC responses and server-initiated notifications.
type incomingMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ResponseError  `json:"error,omitempty"`
}

// -------------------------------------------------------------------
// Client
// -------------------------------------------------------------------

// Client is a persistent LSP subprocess client for one language server.
type Client struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	reader    *bufio.Reader
	mu        sync.Mutex
	nextID    atomic.Int32
	rootURI   string
	startAt   time.Time
	diagStore sync.Map       // key: URI (string) → value: []Diagnostic
	openFiles map[string]int // ref-count of open documents; guarded by mu
}

// newClient starts the given language server binary and performs the
// initialize / initialized handshake.
//
// rootDir is the language-specific workspace root (detected via detectLangRoot).
// It is used as the process working directory so that servers which rely on CWD
// for runtime-path discovery (pyright, lua-language-server) resolve modules
// relative to the project root, and as the LSP rootUri / workspaceFolders URI
// sent during initialization.
func newClient(ctx context.Context, binary string, args []string, rootDir string) (*Client, error) {
	absRoot, _ := filepath.Abs(rootDir)

	cmd := exec.CommandContext(ctx, binary, args...) //nolint:gosec
	cmd.Dir = absRoot                                // anchor the server process to the workspace root
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("lsp: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("lsp: stdout pipe: %w", err)
	}
	cmd.Stderr = nil // discard LSP server log noise

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("lsp: start %s: %w", binary, err)
	}

	c := &Client{
		cmd:       cmd,
		stdin:     stdin,
		reader:    bufio.NewReaderSize(stdout, 1<<20),
		rootURI:   PathToURI(absRoot),
		startAt:   time.Now(),
		openFiles: make(map[string]int),
	}

	if err := c.initialize(ctx); err != nil {
		cmd.Process.Kill() //nolint:errcheck
		return nil, fmt.Errorf("lsp: initialize %s: %w", binary, err)
	}
	return c, nil
}

// nextRequestID returns a monotonically increasing request ID.
func (c *Client) nextRequestID() int {
	return int(c.nextID.Add(1))
}

// send writes a JSON-RPC request and waits for the matching response.
// While waiting, any textDocument/publishDiagnostics notifications that arrive
// are captured in c.diagStore. The caller must hold c.mu.
func (c *Client) send(ctx context.Context, method string, params any, result any) error {
	id := c.nextRequestID()
	req := RequestMessage{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	if err := WriteMessage(c.stdin, req); err != nil {
		return err
	}

	// Ensure the context has a deadline; if not, impose callTimeout.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, callTimeout)
		defer cancel()
	}

	// Read responses until we find the one matching our ID.
	// ReadMessage blocks on a bufio.Reader (pipe), so we run it in a goroutine
	// and select against ctx.Done() to honour the deadline.
	type readResult struct {
		msg incomingMessage
		err error
	}

	for {
		ch := make(chan readResult, 1)
		go func() {
			var msg incomingMessage
			err := ReadMessage(c.reader, &msg)
			ch <- readResult{msg, err}
		}()

		select {
		case <-ctx.Done():
			return fmt.Errorf("lsp: %s timed out: %w", method, ctx.Err())
		case r := <-ch:
			if r.err != nil {
				return r.err
			}
			// Server-initiated notification (no ID field) — handle and keep reading.
			if r.msg.ID == nil {
				c.handleNotification(r.msg)
				continue
			}
			// Response for a different in-flight request — keep reading.
			if *r.msg.ID != id {
				continue
			}
			// Matched response.
			if r.msg.Error != nil {
				return fmt.Errorf("lsp: %s error %d: %s", method, r.msg.Error.Code, r.msg.Error.Message)
			}
			if result != nil && r.msg.Result != nil {
				return json.Unmarshal(r.msg.Result, result)
			}
			return nil
		}
	}
}

// handleNotification dispatches a server-initiated notification.
// Called inside the send loop while c.mu is held.
func (c *Client) handleNotification(msg incomingMessage) {
	if msg.Method != "textDocument/publishDiagnostics" || len(msg.Params) == 0 {
		return
	}
	var p PublishDiagnosticsParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		return
	}
	// Store the full diagnostic list for the URI, overwriting any previous value
	// (the server always sends the complete current set for a file).
	c.diagStore.Store(p.URI, p.Diagnostics)
}

// DrainDiagnostics returns every publishDiagnostics notification captured
// during send calls and clears the internal cache. Keys are document URIs.
func (c *Client) DrainDiagnostics() map[string][]Diagnostic {
	out := make(map[string][]Diagnostic)
	c.diagStore.Range(func(k, v any) bool {
		uri, _ := k.(string)
		diags, _ := v.([]Diagnostic)
		out[uri] = diags
		c.diagStore.Delete(k)
		return true
	})
	return out
}

// notify sends a JSON-RPC notification (no response expected).
func (c *Client) notify(method string, params any) error {
	n := NotificationMessage{JSONRPC: "2.0", Method: method, Params: params}
	return WriteMessage(c.stdin, n)
}

// initialize sends the LSP initialize request + initialized notification.
func (c *Client) initialize(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	params := InitializeParams{
		ProcessID: os.Getpid(),
		RootURI:   c.rootURI,
		WorkspaceFolders: []WorkspaceFolder{
			{URI: c.rootURI, Name: filepath.Base(URIToPath(c.rootURI))},
		},
		Capabilities: ClientCapabilities{},
	}
	var result InitializeResult
	if err := c.send(ctx, "initialize", params, &result); err != nil {
		return err
	}
	return c.notify("initialized", map[string]any{})
}

// didOpen notifies the server that a file is open.
func (c *Client) didOpen(uri, langID, text string) error {
	return c.notify("textDocument/didOpen", DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{URI: uri, LanguageID: langID, Version: 1, Text: text},
	})
}

// didClose notifies the server that a file is closed.
func (c *Client) didClose(uri string) error {
	return c.notify("textDocument/didClose", DidCloseTextDocumentParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
	})
}

// openFile increments the open ref-count for uri. The actual didOpen
// notification is sent only the first time (count goes 0 → 1). Subsequent
// workers that need the same file just increment the counter. Caller must NOT
// hold c.mu.
func (c *Client) openFile(uri, langID, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.openFiles[uri]++
	if c.openFiles[uri] == 1 {
		return c.didOpen(uri, langID, text)
	}
	return nil
}

// closeFile decrements the open ref-count for uri. The actual didClose
// notification is sent only when the count reaches zero. Caller must NOT
// hold c.mu.
func (c *Client) closeFile(uri string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.openFiles[uri]--
	if c.openFiles[uri] <= 0 {
		delete(c.openFiles, uri)
		c.didClose(uri) //nolint:errcheck
	}
}

// references calls textDocument/references for the given position.
func (c *Client) references(ctx context.Context, uri string, pos Position) ([]Location, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	var locs []Location
	err := c.send(ctx, "textDocument/references", ReferencesParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     pos,
		Context:      ReferenceContext{IncludeDeclaration: false},
	}, &locs)
	return locs, err
}

// implementations calls textDocument/implementation for the given position.
func (c *Client) implementations(ctx context.Context, uri string, pos Position) ([]Location, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	var locs []Location
	err := c.send(ctx, "textDocument/implementation", ImplementationParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     pos,
	}, &locs)
	return locs, err
}

// Close shuts down the LSP subprocess.
func (c *Client) Close() {
	c.notify("shutdown", nil) //nolint:errcheck
	c.notify("exit", nil)     //nolint:errcheck
	c.stdin.Close()           //nolint:errcheck
	c.cmd.Wait()              //nolint:errcheck
}

// warmupWait blocks until the adaptive warmup period has elapsed.
func (c *Client) warmupWait() {
	elapsed := time.Since(c.startAt)
	if remaining := warmupDuration - elapsed; remaining > 0 {
		time.Sleep(remaining)
	}
}

// -------------------------------------------------------------------
// Service — manages one Client per language, drives enrichment
// -------------------------------------------------------------------

// Service manages per-language LSP clients and drives the enrichment phase.
type Service struct {
	gitRoot string
	pm      *pkgmgr.Manager
	clients map[string]*Client // keyed by langID
	mu      sync.Mutex
}

// NewService creates a new Service for the given git root.
func NewService(gitRoot string, pm *pkgmgr.Manager) *Service {
	return &Service{
		gitRoot: gitRoot,
		pm:      pm,
		clients: make(map[string]*Client),
	}
}

// Prewarm starts the LSP client for each given language ID concurrently and
// waits until all warmup periods have elapsed. Intended to be called in the
// background (via go) before a scan so that clients are warm by the time
// Enrich runs. It is a no-op for any language whose server binary cannot be
// resolved.
func (s *Service) Prewarm(ctx context.Context, langIDs []string) {
	var wg sync.WaitGroup
	for _, id := range langIDs {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := s.getClient(ctx, id)
			if err != nil {
				return
			}
			c.warmupWait()
		}()
	}
	wg.Wait()
}

// Close shuts down all active LSP clients.
func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.clients {
		c.Close()
	}
	s.clients = make(map[string]*Client)
}

// getClient returns an existing client for langID, or starts a new one.
func (s *Service) getClient(ctx context.Context, langID string) (*Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if c, ok := s.clients[langID]; ok {
		return c, nil
	}

	// Find the language server spec.
	var spec *langServer
	for i := range languageServers {
		if languageServers[i].langID == langID {
			spec = &languageServers[i]
			break
		}
	}
	if spec == nil {
		return nil, fmt.Errorf("lsp: no server for language %q", langID)
	}

	// Resolve binary: PATH → pkgmgr cache → auto-download.
	binary, err := s.pm.ResolveBinary(ctx, spec.binary)
	if err != nil {
		return nil, fmt.Errorf("lsp: resolve %s: %w", spec.binary, err)
	}

	langRoot := detectLangRoot(s.gitRoot, langID)
	c, err := newClient(ctx, binary, serverArgs(spec, langRoot), langRoot)
	if err != nil {
		return nil, err
	}
	s.clients[langID] = c
	return c, nil
}

// Enrich takes the nodes produced by the scanner, queries each language server
// for references and implementations, and returns the resulting edges.
// Any textDocument/publishDiagnostics notifications received during enrichment
// are captured inside each Client and can be retrieved via DrainDiagnostics.
func (s *Service) Enrich(ctx context.Context, nodes []graph.Node, store *graph.Store) ([]graph.Edge, error) {
	if len(nodes) == 0 {
		return nil, nil
	}

	// Group nodes by language so we open each file once per language.
	byLang := make(map[string][]graph.Node)
	for _, n := range nodes {
		ext := filepath.Ext(n.FilePath)
		langID, ok := scanner.LangIDForExt(ext)
		if !ok {
			continue
		}
		byLang[langID] = append(byLang[langID], n)
	}

	// Collect all edges from all languages.
	var (
		edgeMu   sync.Mutex
		allEdges []graph.Edge
	)

	// Enrich all languages concurrently. Each language gets its own worker pool.
	var wg sync.WaitGroup
	for langID, langNodes := range byLang {
		langID := langID
		langNodes := langNodes
		wg.Add(1)
		go func() {
			defer wg.Done()

			client, err := s.getClient(ctx, langID)
			if err != nil {
				// Language server unavailable — skip this language.
				return
			}

			// Adaptive warmup: wait until the server has had time to index.
			client.warmupWait()

			// Fan out enrichment across the worker pool.
			work := make(chan graph.Node, len(langNodes))
			for _, n := range langNodes {
				work <- n
			}
			close(work)

			var innerWg sync.WaitGroup
			for i := 0; i < workerCount; i++ {
				innerWg.Add(1)
				go func() {
					defer innerWg.Done()
					for n := range work {
						if ctx.Err() != nil {
							return
						}
						edges := s.enrichNode(ctx, client, langID, n, store)
						if len(edges) > 0 {
							edgeMu.Lock()
							allEdges = append(allEdges, edges...)
							edgeMu.Unlock()
						}
					}
				}()
			}
			innerWg.Wait()
		}()
	}
	wg.Wait()

	return allEdges, ctx.Err()
}

// DrainDiagnostics collects and clears all publishDiagnostics notifications
// that were captured across every active LSP client during the last Enrich
// call. The returned map key is the document URI as sent by the language server.
func (s *Service) DrainDiagnostics() map[string][]Diagnostic {
	s.mu.Lock()
	defer s.mu.Unlock()

	merged := make(map[string][]Diagnostic)
	for _, c := range s.clients {
		for uri, diags := range c.DrainDiagnostics() {
			merged[uri] = diags
		}
	}
	return merged
}

// enrichNode opens the file, queries references/implementations for node n,
// and returns all edges found.
func (s *Service) enrichNode(ctx context.Context, c *Client, langID string, n graph.Node, store *graph.Store) []graph.Edge {
	uri := PathToURI(n.FilePath)
	text, err := os.ReadFile(n.FilePath)
	if err != nil {
		return nil
	}

	// Open the file in the language server. Ref-counted: didOpen is sent only
	// the first time this URI is seen by any worker; didClose fires only after
	// the last worker processing a node in this file calls closeFile.
	if err := c.openFile(uri, langID, string(text)); err != nil {
		return nil
	}
	defer c.closeFile(uri)

	// Use the position of the name identifier (0-indexed for LSP).
	// NameLine/NameCol point at the symbol name token, which gopls requires
	// for textDocument/references to return results (not the declaration keyword).
	pos := Position{
		Line:      n.NameLine - 1,
		Character: n.NameCol - 1,
	}

	var edges []graph.Edge

	// references: (caller) --references--> (n)
	refs, _ := c.references(ctx, uri, pos)
	for _, loc := range refs {
		refPath := URIToPath(loc.URI)
		refLine := loc.Range.Start.Line + 1
		refCol := loc.Range.Start.Character + 1
		caller, err := store.FindNode(ctx, refPath, refLine, refCol)
		if err != nil || caller == nil {
			continue
		}
		if caller.ID == n.ID {
			continue // skip self-reference
		}
		edges = append(edges, graph.Edge{
			SourceID: caller.ID,
			TargetID: n.ID,
			Relation: graph.RelationReferences,
		})
	}

	// implementations: (impl) --implements--> (n)  (only for interface-kind nodes)
	if n.Kind == "interface" || n.Kind == "type" {
		impls, _ := c.implementations(ctx, uri, pos)
		for _, loc := range impls {
			implPath := URIToPath(loc.URI)
			implLine := loc.Range.Start.Line + 1
			implCol := loc.Range.Start.Character + 1
			implNode, err := store.FindNode(ctx, implPath, implLine, implCol)
			if err != nil || implNode == nil {
				continue
			}
			if implNode.ID == n.ID {
				continue
			}
			edges = append(edges, graph.Edge{
				SourceID: implNode.ID,
				TargetID: n.ID,
				Relation: graph.RelationImplements,
			})
		}
	}

	return edges
}
