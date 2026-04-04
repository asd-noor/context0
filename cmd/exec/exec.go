// Package exec implements the `context0 exec` command.
//
// exec runs a Python script via the sidecar's Ralph-loop (uv run + self-correction).
// The script is read from a file argument or from stdin when "-" is given.
package exec

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"context0/internal/sidecar"
)

// NewCmd returns the cobra command for `context0 exec`.
func NewCmd(projectDir *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec <script-file|-|inline script>",
		Short: "Execute a Python script via the sidecar (with Ralph-loop self-correction)",
		Long: `exec runs a Python script through the sidecar's Ralph-loop:

  context0 exec script.py          # run a file
  context0 exec -                  # read script from stdin
  context0 exec 'print("hello")'   # inline one-liner

On failure the model automatically attempts to repair the script up to 2 times.
Requires the sidecar to be running (context0 --daemon).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			script, err := readScript(args[0])
			if err != nil {
				return err
			}

			raw, err := sidecar.SendRaw(sidecar.Request{
				"cmd":     "exec",
				"script":  script,
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
				return fmt.Errorf("exec: decode response: %w", err)
			}

			// Always print captured output.
			if resp.Output != "" {
				fmt.Print(resp.Output)
			}
			if !resp.OK {
				return fmt.Errorf("exec failed: %s", resp.Error)
			}
			return nil
		},
	}
	return cmd
}

// readScript resolves the script source from a file path, stdin ("-"), or an
// inline string (if the argument contains a newline or starts with a Python
// keyword / "print(").
func readScript(arg string) (string, error) {
	if arg == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("exec: read stdin: %w", err)
		}
		return string(data), nil
	}

	// Try to open as a file first.
	if data, err := os.ReadFile(arg); err == nil {
		return string(data), nil
	}

	// Treat as an inline script string.
	return arg, nil
}
