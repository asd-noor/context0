// Package ask implements the `context0 ask` command.
//
// ask sends a natural-language query to the Python sidecar's orchestration
// loop, which plans CLI commands, executes them, and returns a compressed answer.
package ask

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"context0/internal/sidecar"
)

// NewCmd returns the cobra command for `context0 ask`.
func NewCmd(projectDir *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ask <query>",
		Short: "Natural-language query orchestrated across all context0 engines",
		Long: `ask sends a query to the sidecar's orchestration loop.

The sidecar plans which context0 commands to run (memory, codemap, agenda),
executes them, and compresses the results into a single answer.

Example:
  context0 ask "What caching strategy does this project use?"

Requires the sidecar to be running (context0 --start-sidecar).`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Join all args so the user doesn't need quotes.
			query := joinArgs(args)

			raw, err := sidecar.SendRaw(sidecar.Request{
				"cmd":     "ask",
				"query":   query,
				"project": *projectDir,
			})
			if err != nil {
				return err
			}

			var resp struct {
				OK     bool   `json:"ok"`
				Answer string `json:"answer"`
				Error  string `json:"error,omitempty"`
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				return fmt.Errorf("ask: decode response: %w", err)
			}
			if !resp.OK {
				return fmt.Errorf("ask: sidecar error: %s", resp.Error)
			}
			fmt.Println(resp.Answer)
			return nil
		},
	}
	return cmd
}

func joinArgs(args []string) string {
	result := ""
	for i, a := range args {
		if i > 0 {
			result += " "
		}
		result += a
	}
	return result
}
