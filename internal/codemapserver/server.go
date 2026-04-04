// Package codemapserver wires the codemap engine together: scanning, LSP
// enrichment, file watching, index lifecycle, and MCP tool exposure.
package codemapserver

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"context0/internal/db"
	"context0/internal/graph"
	"context0/internal/lsp"
	"context0/internal/pkgmgr"
	"context0/internal/scanner"
	"context0/internal/watcher"
)

// saveDiagnostics drains the LSP service diagnostic cache, converts every
// entry to graph types, resolves the enclosing symbol node for each diagnostic,
// and persists everything to the store.
func (srv *Server) saveDiagnostics(ctx context.Context) error {
	raw := srv.lspSvc.DrainDiagnostics()
	for uri, lspDiags := range raw {
		filePath := lsp.URIToPath(uri)

		var diags []graph.Diagnostic
		var edges []graph.DiagnosticEdge

		for _, d := range lspDiags {
			line := d.Range.Start.Line + 1     // LSP is 0-indexed
			col := d.Range.Start.Character + 1 // LSP is 0-indexed
			gd := graph.Diagnostic{
				ID:       graph.DiagnosticID(filePath, line, col, d.Message),
				FilePath: filePath,
				Line:     line,
				Col:      col,
				Severity: d.Severity,
				Code:     d.Code,
				Source:   d.Source,
				Message:  d.Message,
			}
			diags = append(diags, gd)

			// Best-effort: link to the smallest enclosing symbol node.
			if n, err := srv.store.FindNode(ctx, filePath, line, col); err == nil && n != nil {
				edges = append(edges, graph.DiagnosticEdge{
					DiagnosticID: gd.ID,
					NodeID:       n.ID,
				})
			}
		}

		if err := srv.store.UpsertDiagnosticsForFile(ctx, filePath, diags); err != nil {
			return fmt.Errorf("upsert diagnostics for %s: %w", filePath, err)
		}
		if err := srv.store.BulkUpsertDiagnosticEdges(ctx, edges); err != nil {
			return fmt.Errorf("upsert diagnostic edges for %s: %w", filePath, err)
		}
	}
	return nil
}

// IndexStatus represents the lifecycle state of the codemap index.
type IndexStatus int

const (
	IndexStatusIdle       IndexStatus = iota
	IndexStatusInProgress             // scanning + enriching
	IndexStatusReady                  // index complete, queries served
	IndexStatusFailed                 // scan or enrich error
)

func (s IndexStatus) String() string {
	switch s {
	case IndexStatusIdle:
		return "idle"
	case IndexStatusInProgress:
		return "in_progress"
	case IndexStatusReady:
		return "ready"
	case IndexStatusFailed:
		return "failed"
	default:
		return "unknown"
	}
}

const waitForIndexTimeout = 30 * time.Second

// Server holds all codemap engine state.
type Server struct {
	rootDir string
	store   *graph.Store
	sc      *scanner.Scanner
	lspSvc  *lsp.Service

	mu         sync.Mutex
	status     IndexStatus
	startedAt  time.Time
	finishedAt time.Time
	indexErr   error
	done       chan struct{} // closed when status transitions to Ready or Failed
	closeOnce  *sync.Once    // guards close(done); replaced alongside done
}

// New creates and starts a Server for the given project root. The initial
// index begins immediately in a background goroutine. The file watcher runs
// until ctx is cancelled; idle-timeout events from the watcher are suppressed
// (a no-op cancel is used) because the MCP server process lifetime governs
// shutdown.
//
// srcRoot overrides the directory that is scanned for source files. When empty
// it defaults to rootDir (the git root of the project).
func New(ctx context.Context, rootDir, srcRoot string) (*Server, error) {
	return newServer(ctx, rootDir, srcRoot, func() {})
}

// NewWatch is like New but passes cancel to the watcher so that the watcher's
// idle-timeout fires cancel(), unblocking the caller's <-ctx.Done().
// Use this when running context0 codemap --watch <dir>.
//
// srcRoot overrides the directory that is scanned for source files. When empty
// it defaults to rootDir (the git root of the project).
func NewWatch(ctx context.Context, cancel context.CancelFunc, rootDir, srcRoot string) (*Server, error) {
	return newServer(ctx, rootDir, srcRoot, cancel)
}

// newServer is the shared constructor used by New and NewWatch.
//
// rootDir determines both the database location (via its git root) and the
// default scan directory. srcRoot, when non-empty, overrides the directory
// walked by the scanner, the LSP workspace root, and the file watcher root,
// without affecting where the index database is stored.
func newServer(ctx context.Context, rootDir, srcRoot string, cancel context.CancelFunc) (*Server, error) {
	absRoot := FindGitRoot(rootDir)

	// Resolve the scan root. A bare basename (no path separator) is treated as
	// a DB-naming label only — scanning falls back to absRoot. A relative or
	// absolute path (contains a separator) is expanded and used as the scan dir.
	absScanRoot := absRoot
	if srcRoot != "" && strings.ContainsRune(srcRoot, filepath.Separator) {
		abs, err := filepath.Abs(srcRoot)
		if err != nil {
			return nil, fmt.Errorf("codemapserver: resolve src-root: %w", err)
		}
		absScanRoot = abs
	}

	store, err := graph.Open(absRoot, db.CodeMapDBName(srcRoot))
	if err != nil {
		return nil, fmt.Errorf("codemapserver: open store: %w", err)
	}

	pm := pkgmgr.New()
	sc := scanner.New(absScanRoot)
	lspSvc := lsp.NewService(absScanRoot, pm)

	srv := &Server{
		rootDir:   absScanRoot,
		store:     store,
		sc:        sc,
		lspSvc:    lspSvc,
		status:    IndexStatusIdle,
		done:      make(chan struct{}),
		closeOnce: &sync.Once{},
	}

	// Start initial full index in the background.
	go srv.runIndex(ctx)

	// Start file watcher.
	go func() {
		w, err := watcher.New(absScanRoot, sc, lspSvc, store)
		if err != nil {
			return
		}
		w.Run(ctx, cancel)
	}()

	return srv, nil
}

// Close shuts down the server's resources.
func (srv *Server) Close() {
	srv.lspSvc.Close()
	srv.store.Close() //nolint:errcheck
}

// Status returns the current index status plus duration.
func (srv *Server) Status() (IndexStatus, time.Duration, error) {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	var dur time.Duration
	switch srv.status {
	case IndexStatusInProgress:
		dur = time.Since(srv.startedAt)
	case IndexStatusReady, IndexStatusFailed:
		dur = srv.finishedAt.Sub(srv.startedAt)
	}
	return srv.status, dur, srv.indexErr
}

// WaitForIndex blocks until the index is Ready or Failed (or ctx expires).
// It returns an error if the index failed or the context expired.
func (srv *Server) WaitForIndex(ctx context.Context) error {
	// Quick path — already done.
	srv.mu.Lock()
	done := srv.done
	st := srv.status
	srv.mu.Unlock()

	if st == IndexStatusReady {
		return nil
	}
	if st == IndexStatusFailed {
		return srv.indexErr
	}

	timeout := time.NewTimer(waitForIndexTimeout)
	defer timeout.Stop()

	select {
	case <-done:
		srv.mu.Lock()
		err := srv.indexErr
		srv.mu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-timeout.C:
		return fmt.Errorf("codemapserver: timed out waiting for index")
	}
}

// ForceIndex triggers a full re-index. Returns an error if one is already running.
func (srv *Server) ForceIndex(ctx context.Context) error {
	srv.mu.Lock()
	if srv.status == IndexStatusInProgress {
		srv.mu.Unlock()
		return fmt.Errorf("codemapserver: index already in progress")
	}
	// Reset the done channel and its Once for callers waiting on WaitForIndex.
	srv.done = make(chan struct{})
	srv.closeOnce = &sync.Once{}
	srv.mu.Unlock()

	go srv.runIndex(ctx)
	return nil
}

// runIndex performs a full scan + enrichment cycle, updating status.
func (srv *Server) runIndex(ctx context.Context) {
	srv.mu.Lock()
	srv.status = IndexStatusInProgress
	srv.startedAt = time.Now()
	srv.indexErr = nil
	done := srv.done      // capture before releasing the lock
	once := srv.closeOnce // capture the Once paired with this channel
	srv.mu.Unlock()

	err := srv.doIndex(ctx)

	srv.mu.Lock()
	srv.finishedAt = time.Now()
	if err != nil {
		srv.status = IndexStatusFailed
		srv.indexErr = err
	} else {
		srv.status = IndexStatusReady
	}
	srv.mu.Unlock()

	once.Do(func() { close(done) })
}

// doIndex runs the actual scan + enrich + store cycle, including diagnostics.
func (srv *Server) doIndex(ctx context.Context) error {
	if err := srv.store.Clear(ctx); err != nil {
		return fmt.Errorf("clear store: %w", err)
	}

	nodes, err := srv.sc.Scan(ctx, srv.rootDir)
	if err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	if err := srv.store.BulkUpsertNodes(ctx, nodes); err != nil {
		return fmt.Errorf("upsert nodes: %w", err)
	}

	edges, err := srv.lspSvc.Enrich(ctx, nodes, srv.store)
	if err != nil {
		return fmt.Errorf("enrich: %w", err)
	}

	if err := srv.store.BulkUpsertEdges(ctx, edges); err != nil {
		return fmt.Errorf("upsert edges: %w", err)
	}

	// Persist any LSP diagnostics that arrived during enrichment.
	if err := srv.saveDiagnostics(ctx); err != nil {
		return fmt.Errorf("save diagnostics: %w", err)
	}

	return nil
}

// Store returns the underlying graph store (for direct queries from tools).
func (srv *Server) Store() *graph.Store { return srv.store }
