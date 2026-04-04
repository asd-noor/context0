// Package cmddocslib implements the `context0 docs-lib` subcommand.
//
// docs-lib fetches up-to-date library documentation from Context7 by resolving
// a human-readable library name to a Context7 ID and then querying the
// documentation for the given topic.  The result is printed as markdown to
// stdout.
//
// Requires the Python sidecar to be running (`context0 --daemon`).
package cmddocslib

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"context0/internal/sidecar"
)

// NewCmd returns the cobra command for `context0 docs-lib`.
func NewCmd(_ *string) *cobra.Command {
	var tokens int

	cmd := &cobra.Command{
		Use:   "docs-lib <library> <question>",
		Short: "Fetch library documentation from Context7",
		Long: `Fetch up-to-date documentation for any library or framework using Context7.

Resolves the library name to a Context7 ID, then retrieves documentation
focused on the given question or topic.  Output is printed as markdown.

Examples:
  context0 docs-lib react "how does useEffect work"
  context0 docs-lib "go cobra" "persistent flags"
  context0 docs-lib numpy "broadcasting rules"

Requires the sidecar to be running (context0 --daemon).`,

		Args: cobra.MinimumNArgs(2),

		RunE: func(cmd *cobra.Command, args []string) error {
			library := args[0]
			// Join remaining args so the user doesn't need quotes for the question.
			question := strings.Join(args[1:], " ")
			return run(library, question, tokens)
		},
	}

	cmd.Flags().IntVarP(&tokens, "tokens", "n", 5000,
		"Maximum documentation tokens to return")

	return cmd
}

func run(library, question string, tokens int) error {
	raw, err := sidecar.SendRaw(sidecar.Request{
		"cmd":     "context7",
		"library": library,
		"query":   question,
		"tokens":  tokens,
	})
	if err != nil {
		return err
	}

	var resp struct {
		OK        bool   `json:"ok"`
		Docs      string `json:"docs"`
		LibraryID string `json:"library_id"`
		Error     string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("docs-lib: decode response: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("docs-lib: %s", resp.Error)
	}

	fmt.Printf("# %s  (%s)\n\n%s\n", library, resp.LibraryID, resp.Docs)
	return nil
}
