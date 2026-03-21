// Package codemapserver wires the codemap engine together: scanning, LSP
// enrichment, file watching, index lifecycle, and MCP tool exposure.
package codemapserver

import (
	"context"
	"fmt"
	"sync"
	"time"

	"context0/internal/graph"
	"context0/internal/lsp"
	"context0/internal/pkgmgr"
	"context0/internal/scanner"
	"context0/internal/watcher"
	"context0/util"
)

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
func New(ctx context.Context, rootDir string) (*Server, error) {
	return newServer(ctx, rootDir, func() {})
}

// NewWatch is like New but passes cancel to the watcher so that the watcher's
// idle-timeout fires cancel(), unblocking the caller's <-ctx.Done().
// Use this when running context0 codemap --watch <dir>.
func NewWatch(ctx context.Context, cancel context.CancelFunc, rootDir string) (*Server, error) {
	return newServer(ctx, rootDir, cancel)
}

// newServer is the shared constructor used by New and NewWatch.
func newServer(ctx context.Context, rootDir string, cancel context.CancelFunc) (*Server, error) {
	absRoot := util.FindGitRoot(rootDir)

	store, err := graph.Open(absRoot)
	if err != nil {
		return nil, fmt.Errorf("codemapserver: open store: %w", err)
	}

	pm := pkgmgr.New()
	sc := scanner.New(absRoot)
	lspSvc := lsp.NewService(absRoot, pm)

	srv := &Server{
		rootDir:   absRoot,
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
		w, err := watcher.New(absRoot, sc, lspSvc, store)
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

// doIndex runs the actual scan + enrich + store cycle.
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

	return nil
}

// Store returns the underlying graph store (for direct queries from tools).
func (srv *Server) Store() *graph.Store { return srv.store }
