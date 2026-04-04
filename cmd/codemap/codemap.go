// Package codemap provides the `context0 codemap` CLI sub-commands.
//
// CLI-first design — every codemap capability is a direct CLI command.
//
// All sub-commands share the parent-level --project flag (default: CWD):
//
//	context0 codemap [--project <dir>] watch        — start the daemon in the background
//	context0 codemap [--project <dir>] index        — (re)build the symbol index
//	context0 codemap [--project <dir>] status       — show current index status
//	context0 codemap [--project <dir>] outline <file> — list symbols defined in a file
//	context0 codemap [--project <dir>] find   <name>  — locate a symbol across the project
//	context0 codemap [--project <dir>] impact  <name> — show transitive impact of a symbol
package codemap

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"context0/internal/codemapserver"
	"context0/internal/daemon"
	"context0/internal/db"
	"context0/internal/graph"
	"context0/internal/scanner"
	"context0/internal/sidecar"
)

// NewCmd returns the root `codemap` cobra command with all sub-commands attached.
func NewCmd(projectDir *string) *cobra.Command {
	var srcRoot string
	cmd := &cobra.Command{
		Use:   "codemap",
		Short: "Code Exploration Engine",
	}

	// --project is inherited from the root context0 command via PersistentFlags.
	cmd.PersistentFlags().StringVar(&srcRoot, "src-root", "", "Override the root directory for source scanning (default: basename of project root)")

	// Default --src-root to the basename of the project root so the codemap DB
	// is named after the project (e.g. "myrepo-ctx0.sqlite"). The value is set
	// here rather than as a static flag default because projectDir is resolved
	// at flag-parse time, after NewCmd returns.
	cmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if srcRoot == "" {
			srcRoot = filepath.Base(*projectDir)
		}
		return nil
	}

	cmd.AddCommand(
		newWatchCmd(projectDir, &srcRoot),
		newIndexCmd(projectDir, &srcRoot),
		newStatusCmd(projectDir, &srcRoot),
		newSymbolsCmd(projectDir, &srcRoot),
		newSymbolCmd(projectDir, &srcRoot),
		newImpactCmd(projectDir, &srcRoot),
		newDiagnosticsCmd(projectDir, &srcRoot),
		newDiscoverCmd(projectDir),
	)
	return cmd
}

// gitRoot resolves the git root from dir.
func gitRoot(dir string) string {
	return codemapserver.FindGitRoot(dir)
}

// openStore opens the existing graph store for a project in read-only mode.
// Returns graph.ErrNotIndexed if no index has been built yet.
func openStore(dir, srcRoot string) (*graph.Store, error) {
	store, err := graph.OpenReadOnly(gitRoot(dir), db.CodeMapDBName(srcRoot))
	if err != nil {
		return nil, err
	}
	return store, nil
}

// ── watch ─────────────────────────────────────────────────────────────────────

func newWatchCmd(projectDir, srcRoot *string) *cobra.Command {
	var daemonMode bool
	var foreground bool
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Start the codemap daemon in the background (auto-stops after 5 min of inactivity)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case daemonMode:
				return runWatchDaemon(*projectDir, *srcRoot)
			case foreground:
				return runWatchForeground(*projectDir, *srcRoot)
			default:
				return runWatch(*projectDir, *srcRoot)
			}
		},
	}
	cmd.Flags().BoolVar(&daemonMode, "daemon", false, "Run as background daemon with idle-timeout (internal use)")
	cmd.Flags().MarkHidden("daemon") //nolint:errcheck
	cmd.Flags().BoolVar(&foreground, "foreground", false, "Run the watcher in the foreground; lifecycle is managed by the caller (no auto-stop)")
	return cmd
}

// runWatch is called by the user. It checks for an existing daemon, spawns a
// detached background process, and prints the PID file path before returning.
func runWatch(dir, srcRoot string) error {
	root := gitRoot(dir)

	pidPath, err := db.PIDPath(root)
	if err != nil {
		return fmt.Errorf("codemap watch: pid path: %w", err)
	}
	if daemon.IsAlive(pidPath) {
		fmt.Printf("codemap daemon is already running, PIDFILE: %s\n", pidPath)
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("codemap watch: resolve executable: %w", err)
	}
	if err := daemon.Spawn(exe, root, srcRoot); err != nil {
		return fmt.Errorf("codemap watch: %w", err)
	}

	fmt.Printf("Watcher started, PIDFILE: %s\n", pidPath)
	return nil
}

// runWatchDaemon is the blocking daemon loop invoked by the spawned background
// child process via the hidden --daemon flag. The watcher's idle timer is
// active: the process self-terminates after 5 minutes of file inactivity.
func runWatchDaemon(dir, srcRoot string) error {
	root := gitRoot(dir)

	pidPath, err := db.PIDPath(root)
	if err != nil {
		return fmt.Errorf("codemap watch: pid path: %w", err)
	}
	if daemon.IsAlive(pidPath) {
		return nil
	}
	if err := daemon.WritePID(pidPath); err != nil {
		return fmt.Errorf("codemap watch: write pid: %w", err)
	}
	defer daemon.RemovePID(pidPath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv, err := codemapserver.NewWatch(ctx, cancel, root, srcRoot)
	if err != nil {
		return fmt.Errorf("codemap watch: %w", err)
	}
	defer srv.Close()

	<-ctx.Done()
	return nil
}

// runWatchForeground runs the watcher in the foreground. It blocks until
// SIGINT or SIGTERM is received. No idle-timeout auto-stop is applied — the
// invoker is fully responsible for the process lifecycle.
func runWatchForeground(dir, srcRoot string) error {
	root := gitRoot(dir)

	// Register signal handling first, before acquiring any resources, so that
	// a signal arriving at any point is queued rather than terminating the
	// process immediately — defers will always get a chance to clean up.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)

	pidPath, err := db.PIDPath(root)
	if err != nil {
		return fmt.Errorf("codemap watch: pid path: %w", err)
	}
	if daemon.IsAlive(pidPath) {
		fmt.Printf("codemap daemon is already running, PIDFILE: %s\n", pidPath)
		return nil
	}
	if err := daemon.WritePID(pidPath); err != nil {
		return fmt.Errorf("codemap watch: write pid: %w", err)
	}
	defer daemon.RemovePID(pidPath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// codemapserver.New uses a no-op cancel so the watcher's idle timer never
	// triggers a shutdown — the caller controls the lifetime via signals.
	srv, err := codemapserver.New(ctx, root, srcRoot)
	if err != nil {
		return fmt.Errorf("codemap watch: %w", err)
	}
	defer srv.Close()

	fmt.Printf("Watcher running in foreground, PIDFILE: %s\n", pidPath)

	select {
	case <-quit:
	case <-ctx.Done():
	}
	return nil
}

// ── index ─────────────────────────────────────────────────────────────────────

func newIndexCmd(projectDir, srcRoot *string) *cobra.Command {
	return &cobra.Command{
		Use:   "index",
		Short: "Build or rebuild the symbol index",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runIndex(*projectDir, *srcRoot)
		},
	}
}

func runIndex(dir, srcRoot string) error {
	ctx := context.Background()
	srv, err := codemapserver.New(ctx, gitRoot(dir), srcRoot)
	if err != nil {
		return err
	}
	defer srv.Close()

	if err := srv.ForceIndex(ctx); err != nil {
		return err
	}
	if err := srv.WaitForIndex(ctx); err != nil {
		return err
	}

	nodeCount, err := srv.Store().NodeCount(ctx)
	if err != nil {
		return err
	}
	edgeCount, err := srv.Store().EdgeCount(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("indexed  nodes=%d  edges=%d\n", nodeCount, edgeCount)
	return nil
}

// ── status ────────────────────────────────────────────────────────────────────

func newStatusCmd(projectDir, srcRoot *string) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the current codemap index status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(*projectDir, *srcRoot)
		},
	}
}

func runStatus(dir, srcRoot string) error {
	ctx := context.Background()
	store, err := openStore(dir, srcRoot)
	if err != nil {
		return err
	}
	defer store.Close()

	nodeCount, err := store.NodeCount(ctx)
	if err != nil {
		return err
	}
	edgeCount, err := store.EdgeCount(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("nodes=%d  edges=%d\n", nodeCount, edgeCount)
	return nil
}

// ── symbols ───────────────────────────────────────────────────────────────────

func newSymbolsCmd(projectDir, srcRoot *string) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "outline <file>",
		Short: "List all symbols defined in <file>",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSymbols(*projectDir, *srcRoot, args[0], jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

func runSymbols(dir, srcRoot, filePath string, jsonOut bool) error {
	absPath, err := codemapserver.AbsFilePath(filePath)
	if err != nil {
		return err
	}

	root := gitRoot(dir)
	ctx := context.Background()
	store, err := openStore(dir, srcRoot)
	if err != nil {
		return err
	}
	defer store.Close()

	nodes, err := store.GetSymbolsInFile(ctx, absPath)
	if err != nil {
		return err
	}

	if jsonOut {
		return printJSON(nodes)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "KIND\tNAME\tLINES\t(%s)\n", relPath(root, absPath))
	for _, n := range nodes {
		fmt.Fprintf(w, "%s\t%s\t%d-%d\n", n.Kind, n.Name, n.LineStart, n.LineEnd)
	}
	return w.Flush()
}

// ── symbol ────────────────────────────────────────────────────────────────────

func newSymbolCmd(projectDir, srcRoot *string) *cobra.Command {
	var withSource, jsonOut bool
	var lang string
	cmd := &cobra.Command{
		Use:   "find <name>",
		Short: "Locate all definitions of <name> across the project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSymbol(*projectDir, *srcRoot, args[0], lang, withSource, jsonOut)
		},
	}
	cmd.Flags().BoolVar(&withSource, "source", false, "Include source code snippet")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	cmd.Flags().StringVar(&lang, "lang", "", "Filter by language (e.g. go, python, typescript)")
	return cmd
}

func runSymbol(dir, srcRoot, name, lang string, withSource, jsonOut bool) error {
	root := gitRoot(dir)
	ctx := context.Background()
	store, err := openStore(dir, srcRoot)
	if err != nil {
		return err
	}
	defer store.Close()

	// Validate --lang against the known set of language IDs.
	if lang != "" && !scanner.IsKnownLang(lang) {
		return fmt.Errorf("find: unknown language %q (supported: go, python, javascript, typescript, lua, zig)", lang)
	}

	nodes, err := store.GetSymbolLocation(ctx, name, lang)
	if err != nil {
		return err
	}

	results := make([]codemapserver.NodeWithSource, len(nodes))
	for i, n := range nodes {
		results[i] = codemapserver.NodeToWithSource(n, withSource)
	}

	if jsonOut {
		return printJSON(results)
	}

	if !withSource {
		// Compact table view when source is not requested.
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "KIND\tFILE\tLINES\tNAME")
		for _, n := range results {
			fmt.Fprintf(w, "%s\t%s\t%d-%d\t%s\n", n.Kind, relPath(root, n.FilePath), n.LineStart, n.LineEnd, n.Name)
		}
		return w.Flush()
	}

	// With --source: print each result with a fenced code block.
	for i, n := range results {
		if i > 0 {
			fmt.Println()
		}
		rel := relPath(root, n.FilePath)
		fmt.Printf("%s  %s  %s  lines %d-%d\n", n.Kind, n.Name, rel, n.LineStart, n.LineEnd)
		if n.Source != "" {
			lang := langFromExt(n.FilePath)
			fmt.Printf("```%s\n%s```\n", lang, n.Source)
		}
	}
	return nil
}

// ── impact ────────────────────────────────────────────────────────────────────

func newImpactCmd(projectDir, srcRoot *string) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "impact <name>",
		Short: "Show all symbols that transitively depend on <name>",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runImpact(*projectDir, *srcRoot, args[0], jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

func runImpact(dir, srcRoot, name string, jsonOut bool) error {
	root := gitRoot(dir)
	ctx := context.Background()
	store, err := openStore(dir, srcRoot)
	if err != nil {
		return err
	}
	defer store.Close()

	nodes, err := store.FindImpact(ctx, name)
	if err != nil {
		return err
	}

	if jsonOut {
		return printJSON(nodes)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "KIND\tNAME\tFILE\tLINES")
	for _, n := range nodes {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d-%d\n", n.Kind, n.Name, relPath(root, n.FilePath), n.LineStart, n.LineEnd)
	}
	return w.Flush()
}

// ── shared helpers ────────────────────────────────────────────────────────────

// relPath returns a path relative to root. Falls back to the original if it
// cannot be made relative (e.g. different volume on Windows).
func relPath(root, absPath string) string {
	rel, err := filepath.Rel(root, absPath)
	if err != nil {
		return absPath
	}
	return rel
}

// langFromExt returns the Markdown fenced-code-block language tag for a file,
// using scanner.LangIDForExt as the single source of truth.
func langFromExt(filePath string) string {
	id, _ := scanner.LangIDForExt(filepath.Ext(filePath))
	return id
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// ── diagnostics ───────────────────────────────────────────────────────────────

// severityLabel converts an LSP severity integer to a short human-readable tag.
func severityLabel(s int) string {
	switch s {
	case 1:
		return "error"
	case 2:
		return "warning"
	case 3:
		return "info"
	case 4:
		return "hint"
	default:
		return "unknown"
	}
}

func newDiagnosticsCmd(projectDir, srcRoot *string) *cobra.Command {
	var (
		filterFile string
		jsonOut    bool
		severity   int
	)
	cmd := &cobra.Command{
		Use:   "diagnostics",
		Short: "List LSP diagnostics stored in the codemap index",
		Long: `List categorised LSP diagnostics collected during the last index run.

Diagnostics are grouped by file and ordered by severity (error → warning →
info → hint) then by line number. Use --severity to restrict output to a
specific level (1=error, 2=warning, 3=info, 4=hint).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDiagnostics(*projectDir, *srcRoot, filterFile, severity, jsonOut)
		},
	}
	cmd.Flags().StringVar(&filterFile, "file", "", "Restrict output to a single file (absolute or relative path)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	cmd.Flags().IntVar(&severity, "severity", 0, "Filter by severity level (1=error 2=warning 3=info 4=hint); 0 means all")
	return cmd
}

func runDiagnostics(dir, srcRoot, filterFile string, severity int, jsonOut bool) error {
	root := gitRoot(dir)
	ctx := context.Background()

	store, err := openStore(dir, srcRoot)
	if err != nil {
		return err
	}
	defer store.Close()

	var diags []graph.Diagnostic

	if filterFile != "" {
		absFile, err := codemapserver.AbsFilePath(filterFile)
		if err != nil {
			return err
		}
		diags, err = store.GetDiagnosticsForFile(ctx, absFile)
		if err != nil {
			return err
		}
	} else {
		diags, err = store.GetAllDiagnostics(ctx)
		if err != nil {
			return err
		}
	}

	// Apply optional severity filter.
	if severity > 0 {
		filtered := diags[:0]
		for _, d := range diags {
			if d.Severity == severity {
				filtered = append(filtered, d)
			}
		}
		diags = filtered
	}

	if jsonOut {
		return printJSON(diags)
	}

	if len(diags) == 0 {
		fmt.Println("no diagnostics found")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SEVERITY\tFILE\tLINE\tCOL\tSOURCE\tMESSAGE")
	for _, d := range diags {
		rel := relPath(root, d.FilePath)
		src := d.Source
		if src == "" {
			src = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%s\t%s\n",
			severityLabel(d.Severity), rel, d.Line, d.Col, src, d.Message)
	}
	return w.Flush()
}

// ── discover ──────────────────────────────────────────────────────────────────

func newDiscoverCmd(projectDir *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "discover <query>",
		Short: "Natural-language codebase search for non-indexed languages (requires sidecar)",
		Long: `discover generates a targeted fd/rg script via the local model and executes it
with the Ralph-loop self-correction.  Use it for languages not indexed by the
codemap engine, or for ad-hoc structural queries.

Example:
  context0 codemap discover "Find all files that import sqlite3"

Requires the sidecar to be running (context0 --daemon).`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := strings.Join(args, " ")

			raw, err := sidecar.SendRaw(sidecar.Request{
				"cmd":     "discover",
				"query":   query,
				"project": *projectDir,
			})
			if err != nil {
				return err
			}

			var resp struct {
				OK     bool   `json:"ok"`
				Output string `json:"output"`
				Error  string `json:"error,omitempty"`
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				return fmt.Errorf("discover: decode response: %w", err)
			}

			if resp.Output != "" {
				fmt.Print(resp.Output)
			}
			if !resp.OK {
				return fmt.Errorf("discover failed: %s", resp.Error)
			}
			return nil
		},
	}
	return cmd
}
