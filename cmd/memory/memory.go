// Package memory provides the `ctx0 memory` CLI sub-commands.
package memory

import (
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"context0/internal/memory"
)

// NewCmd returns the `memory` sub-command tree.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Manage project memories (save, query, update, delete)",
	}

	cmd.AddCommand(
		newSaveCmd(),
		newQueryCmd(),
		newUpdateCmd(),
		newDeleteCmd(),
	)
	return cmd
}

// projectPath returns the current working directory as the project path.
func projectPath() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

func newSaveCmd() *cobra.Command {
	var category, topic, content string

	cmd := &cobra.Command{
		Use:   "save",
		Short: "Save a new memory",
		RunE: func(cmd *cobra.Command, args []string) error {
			if category == "" || topic == "" || content == "" {
				return fmt.Errorf("--category, --topic, and --content are required")
			}
			eng, err := memory.New(projectPath())
			if err != nil {
				return err
			}
			defer eng.Close()

			doc, err := eng.SaveMemory(category, topic, content)
			if err != nil {
				return err
			}
			fmt.Printf("saved memory id=%d  category=%q  topic=%q\n", doc.ID, doc.Category, doc.Topic)
			return nil
		},
	}

	cmd.Flags().StringVarP(&category, "category", "c", "", "Category (e.g. architecture, fix, feature)")
	cmd.Flags().StringVarP(&topic, "topic", "t", "", "Short topic title")
	cmd.Flags().StringVarP(&content, "content", "C", "", "Memory content")
	return cmd
}

func newQueryCmd() *cobra.Command {
	var topK int

	cmd := &cobra.Command{
		Use:   "query <text>",
		Short: "Hybrid search memories",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := args[0]
			eng, err := memory.New(projectPath())
			if err != nil {
				return err
			}
			defer eng.Close()

			results, err := eng.QueryMemory(query, topK)
			if err != nil {
				return err
			}
			if len(results) == 0 {
				fmt.Println("no results found")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tCATEGORY\tTOPIC\tSCORE\tCONTENT")
			for _, r := range results {
				preview := r.Content
				if len(preview) > 80 {
					preview = preview[:77] + "..."
				}
				fmt.Fprintf(w, "%d\t%s\t%s\t%.4f\t%s\n",
					r.ID, r.Category, r.Topic, r.Score, preview)
			}
			w.Flush()
			return nil
		},
	}

	cmd.Flags().IntVarP(&topK, "top", "k", 3, "Number of results to return")
	return cmd
}

func newUpdateCmd() *cobra.Command {
	var category, topic, content string

	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a memory by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid id: %w", err)
			}
			if category == "" && topic == "" && content == "" {
				return fmt.Errorf("at least one of --category, --topic, --content is required")
			}

			eng, err := memory.New(projectPath())
			if err != nil {
				return err
			}
			defer eng.Close()

			doc, err := eng.UpdateMemory(id, category, topic, content)
			if err != nil {
				return err
			}
			fmt.Printf("updated memory id=%d  category=%q  topic=%q\n", doc.ID, doc.Category, doc.Topic)
			return nil
		},
	}

	cmd.Flags().StringVarP(&category, "category", "c", "", "New category")
	cmd.Flags().StringVarP(&topic, "topic", "t", "", "New topic")
	cmd.Flags().StringVarP(&content, "content", "C", "", "New content")
	return cmd
}

func newDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a memory by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid id: %w", err)
			}

			eng, err := memory.New(projectPath())
			if err != nil {
				return err
			}
			defer eng.Close()

			if err := eng.DeleteMemory(id); err != nil {
				return err
			}
			fmt.Printf("deleted memory id=%d\n", id)
			return nil
		},
	}
}
