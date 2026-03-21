// Package codemap provides the `context0 codemap` CLI sub-commands.
//
// CLI-first design — every codemap capability is a direct CLI command.
//
// All sub-commands share the parent-level --project flag (default: CWD):
//
//	context0 codemap [--project <dir>] watch     — run the daemon (blocks; auto-stops on idle)
//	context0 codemap [--project <dir>] index     — (re)build the symbol index
//	context0 codemap [--project <dir>] status    — show current index status
//	context0 codemap [--project <dir>] symbols <file> — list symbols in a file
//	context0 codemap [--project <dir>] symbol  <name> — find a symbol across the project
//	context0 codemap [--project <dir>] impact  <name> — show transitive impact of a symbol
package codemap

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"context0/internal/codemapserver"
	"context0/internal/daemon"
	"context0/internal/db"
	"context0/internal/graph"
	"context0/util"
)

// NewCmd returns the root `codemap` cobra command with all sub-commands attached.
func NewCmd(projectDir *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "codemap",
		Short: "Code Exploration Engine",
	}

	// --project is inherited from the root context0 command via PersistentFlags.

	cmd.AddCommand(
		newWatchCmd(projectDir),
		newIndexCmd(projectDir),
		newStatusCmd(projectDir),
		newSymbolsCmd(projectDir),
		newSymbolCmd(projectDir),
		newImpactCmd(projectDir),
	)
	return cmd
}

// gitRoot resolves the git root from dir.
func gitRoot(dir string) string {
	return util.FindGitRoot(dir)
}

// openStore opens the existing graph store for a project in read-only mode.
// Returns graph.ErrNotIndexed if no index has been built yet.
func openStore(dir string) (*graph.Store, error) {
	store, err := graph.OpenReadOnly(gitRoot(dir))
	if err != nil {
		return nil, err
	}
	return store, nil
}

// ── watch ─────────────────────────────────────────────────────────────────────

func newWatchCmd(projectDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "watch",
		Short: "Run the codemap daemon (blocks; auto-stops after 5 min of inactivity)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWatch(*projectDir)
		},
	}
}

func runWatch(dir string) error {
	root := gitRoot(dir)

	pidPath, err := db.PIDPath(root)
	if err != nil {
		return fmt.Errorf("codemap watch: pid path: %w", err)
	}
	if daemon.IsAlive(pidPath) {
		fmt.Println("codemap daemon is already running")
		return nil
	}
	if err := daemon.WritePID(pidPath); err != nil {
		return fmt.Errorf("codemap watch: write pid: %w", err)
	}
	defer daemon.RemovePID(pidPath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv, err := codemapserver.NewWatch(ctx, cancel, root)
	if err != nil {
		return fmt.Errorf("codemap watch: %w", err)
	}
	defer srv.Close()

	<-ctx.Done()
	return nil
}

// ── index ─────────────────────────────────────────────────────────────────────

func newIndexCmd(projectDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "index",
		Short: "Build or rebuild the symbol index",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runIndex(*projectDir)
		},
	}
}

func runIndex(dir string) error {
	ctx := context.Background()
	srv, err := codemapserver.New(ctx, gitRoot(dir))
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

func newStatusCmd(projectDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the current codemap index status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(*projectDir)
		},
	}
}

func runStatus(dir string) error {
	ctx := context.Background()
	store, err := openStore(dir)
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

func newSymbolsCmd(projectDir *string) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "symbols <file>",
		Short: "List all symbols in <file>",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSymbols(*projectDir, args[0], jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

func runSymbols(dir, filePath string, jsonOut bool) error {
	absPath, err := codemapserver.AbsFilePath(filePath)
	if err != nil {
		return err
	}

	ctx := context.Background()
	store, err := openStore(dir)
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
	fmt.Fprintln(w, "KIND\tNAME\tLINES")
	for _, n := range nodes {
		fmt.Fprintf(w, "%s\t%s\t%d-%d\n", n.Kind, n.Name, n.LineStart, n.LineEnd)
	}
	return w.Flush()
}

// ── symbol ────────────────────────────────────────────────────────────────────

func newSymbolCmd(projectDir *string) *cobra.Command {
	var withSource, jsonOut bool
	cmd := &cobra.Command{
		Use:   "symbol <name>",
		Short: "Find all locations of <name> in the project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSymbol(*projectDir, args[0], withSource, jsonOut)
		},
	}
	cmd.Flags().BoolVar(&withSource, "source", false, "Include source code snippet")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

func runSymbol(dir, name string, withSource, jsonOut bool) error {
	ctx := context.Background()
	srv, err := codemapserver.New(ctx, gitRoot(dir))
	if err != nil {
		return err
	}
	defer srv.Close()

	results, err := srv.GetSymbolWithSource(ctx, name, withSource)
	if err != nil {
		return err
	}

	if jsonOut {
		return printJSON(results)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "KIND\tFILE\tLINES\tNAME")
	for _, n := range results {
		fmt.Fprintf(w, "%s\t%s\t%d-%d\t%s\n", n.Kind, n.FilePath, n.LineStart, n.LineEnd, n.Name)
		if withSource && n.Source != "" {
			fmt.Fprintf(w, "\t%s\t\t\n", n.Source)
		}
	}
	return w.Flush()
}

// ── impact ────────────────────────────────────────────────────────────────────

func newImpactCmd(projectDir *string) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "impact <name>",
		Short: "Show all symbols that transitively depend on <name>",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runImpact(*projectDir, args[0], jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

func runImpact(dir, name string, jsonOut bool) error {
	ctx := context.Background()
	store, err := openStore(dir)
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
		fmt.Fprintf(w, "%s\t%s\t%s\t%d-%d\n", n.Kind, n.Name, n.FilePath, n.LineStart, n.LineEnd)
	}
	return w.Flush()
}

// ── shared helpers ────────────────────────────────────────────────────────────

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
