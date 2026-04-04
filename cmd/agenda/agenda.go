// Package agenda provides the `context0 agenda` CLI sub-commands.
package agenda

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"context0/internal/agenda"
	"context0/internal/memory"
)

// currentGitBranch returns the name of the current git branch for dir, or ""
// if it cannot be determined (not a repo, git not installed, detached HEAD).
func currentGitBranch(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	branch := strings.TrimSpace(string(out))
	if branch == "HEAD" { // detached HEAD
		return ""
	}
	return branch
}

// wireMemoryHook registers an onClose hook on eng that saves a full agenda
// snapshot to the memory engine when an agenda is auto-deactivated. Errors are
// silently ignored (sidecar may not be running).
func wireMemoryHook(eng *agenda.Engine, projectDir string) {
	eng.SetOnClose(func(a agenda.Agenda) {
		mem, err := memory.New(projectDir)
		if err != nil {
			return
		}
		defer mem.Close()
		topic := a.Title
		if a.GitBranch != "" {
			topic += " (" + a.GitBranch + ")"
		}
		_, _ = mem.SaveMemory("agenda-history", topic, buildMemoryContent(a))
	})
}

// buildMemoryContent renders a completed agenda into a human-readable string
// suitable for storage in the memory engine.
func buildMemoryContent(a agenda.Agenda) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Plan: %s\n", a.Title)
	if a.Description != "" {
		fmt.Fprintf(&sb, "Description: %s\n", a.Description)
	}
	if a.AcceptanceGuard != "" {
		fmt.Fprintf(&sb, "Acceptance criteria: %s\n", a.AcceptanceGuard)
	}
	if a.GitBranch != "" {
		fmt.Fprintf(&sb, "Branch: %s\n", a.GitBranch)
	}
	fmt.Fprintf(&sb, "Priority: %s\n", a.Priority)
	if a.CompletedAt != nil {
		fmt.Fprintf(&sb, "Completed: %s\n", a.CompletedAt.Format("2006-01-02 15:04:05"))
	}
	if len(a.Tasks) > 0 {
		sb.WriteString("Tasks:\n")
		for i, t := range a.Tasks {
			fmt.Fprintf(&sb, "  %d. [%s] %s", i+1, t.Status, t.Details)
			if t.Notes != "" {
				fmt.Fprintf(&sb, "\n     Notes: %s", t.Notes)
			}
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// NewCmd returns the `agenda` sub-command tree.
func NewCmd(projectDir *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agenda",
		Short: "Manage project plans and tasks",
	}
	cmd.AddCommand(
		newPlanCmd(projectDir),
		newTaskCmd(projectDir),
	)
	return cmd
}

// -------------------------------------------------------------------------
// agenda plan ...
// -------------------------------------------------------------------------

func newPlanCmd(projectDir *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Manage plans",
	}
	cmd.AddCommand(
		newPlanListCmd(projectDir),
		newPlanGetCmd(projectDir),
		newPlanCreateCmd(projectDir),
		newPlanSearchCmd(projectDir),
		newPlanUpdateCmd(projectDir),
		newPlanDeleteCmd(projectDir),
		newPlanRestoreCmd(projectDir),
	)
	return cmd
}

// --- plan list ---

func newPlanListCmd(projectDir *string) *cobra.Command {
	var all, deleted bool
	var branch string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List plans (active only by default)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && deleted {
				return fmt.Errorf("--all and --deleted are mutually exclusive")
			}
			eng, err := agenda.New(*projectDir)
			if err != nil {
				return err
			}
			defer eng.Close()

			plans, err := eng.ListAgendas(!all, deleted, branch)
			if err != nil {
				return err
			}
			if len(plans) == 0 {
				fmt.Println("no plans found")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tACTIVE\tPRI\tBRANCH\tTITLE\tDESCRIPTION")
			for _, a := range plans {
				active := "yes"
				if !a.IsActive {
					active = "no"
				}
				desc := a.Description
				if len(desc) > 50 {
					desc = desc[:47] + "..."
				}
				fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n",
					a.ID, active, a.Priority, a.GitBranch, a.Title, desc)
			}
			w.Flush()
			return nil
		},
	}

	cmd.Flags().BoolVar(&all, "all", false, "Include inactive plans")
	cmd.Flags().BoolVar(&deleted, "deleted", false, "Show only soft-deleted plans")
	cmd.Flags().StringVar(&branch, "branch", "", "Filter by git branch (empty = all branches)")
	return cmd
}

// --- plan get ---

// taskStatusSymbol returns the display symbol for a task's status.
func taskStatusSymbol(s agenda.TaskStatus) string {
	switch s {
	case agenda.StatusInProgress:
		return "[→]"
	case agenda.StatusCompleted:
		return "[x]"
	case agenda.StatusBlocked:
		return "[!]"
	default:
		return "[ ]"
	}
}

func newPlanGetCmd(projectDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Retrieve a plan and its full task list",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid id: %w", err)
			}
			eng, err := agenda.New(*projectDir)
			if err != nil {
				return err
			}
			defer eng.Close()

			a, err := eng.GetAgenda(id)
			if err != nil {
				return err
			}

			state := "active"
			if !a.IsActive {
				state = "inactive"
			}
			if a.DeletedAt != nil {
				state = "deleted"
			}
			fmt.Printf("Plan #%d [%s]\n", a.ID, state)
			fmt.Printf("  Title:    %s\n", a.Title)
			fmt.Printf("  Priority: %s\n", a.Priority)
			if a.GitBranch != "" {
				fmt.Printf("  Branch:   %s\n", a.GitBranch)
			}
			if a.Description != "" {
				fmt.Printf("  Description: %s\n", a.Description)
			}
			if a.AcceptanceGuard != "" {
				fmt.Printf("  Done when: %s\n", a.AcceptanceGuard)
			}
			fmt.Printf("  Created:  %s\n", a.CreatedAt.Format("2006-01-02 15:04:05"))
			if a.CompletedAt != nil {
				fmt.Printf("  Completed: %s\n", a.CompletedAt.Format("2006-01-02 15:04:05"))
			}
			if a.DeletedAt != nil {
				fmt.Printf("  Deleted:  %s\n", a.DeletedAt.Format("2006-01-02 15:04:05"))
			}
			fmt.Printf("  Tasks (%d):\n", len(a.Tasks))
			for i, t := range a.Tasks {
				symbol := taskStatusSymbol(t.Status)
				fmt.Printf("    %s #%d: %s\n", symbol, i+1, t.Details)
				if t.Notes != "" {
					fmt.Printf("         %s\n", t.Notes)
				}
			}
			return nil
		},
	}
}

// --- plan create ---

func newPlanCreateCmd(projectDir *string) *cobra.Command {
	var title, description, guard, branch, priority string
	var taskDetails []string

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new plan",
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := agenda.New(*projectDir)
			if err != nil {
				return err
			}
			defer eng.Close()

			if branch == "" {
				branch = currentGitBranch(*projectDir)
			}
			if priority == "" {
				priority = agenda.PriorityNormal
			}

			tasks := make([]agenda.TaskInput, len(taskDetails))
			for i, d := range taskDetails {
				tasks[i] = agenda.TaskInput{Details: d}
			}

			id, err := eng.CreateAgenda(title, description, guard, branch, priority, tasks)
			if err != nil {
				return err
			}
			fmt.Printf("created plan id=%d\n", id)
			return nil
		},
	}

	cmd.Flags().StringVarP(&title, "title", "t", "", "Plan title")
	cmd.Flags().StringVarP(&description, "description", "d", "", "Plan description")
	cmd.Flags().StringVar(&guard, "guard", "", "Acceptance criteria (done-when condition)")
	cmd.Flags().StringVar(&branch, "branch", "", "Git branch (default: auto-detect)")
	cmd.Flags().StringVarP(&priority, "priority", "p", agenda.PriorityNormal, "Priority: high|normal|low")
	cmd.Flags().StringArrayVarP(&taskDetails, "task", "T", nil, "Task details (repeat for multiple tasks)")
	return cmd
}

// --- plan search ---

func newPlanSearchCmd(projectDir *string) *cobra.Command {
	var limit int
	var deleted bool
	var branch string

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Full-text search across plan titles, descriptions, and acceptance criteria",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := agenda.New(*projectDir)
			if err != nil {
				return err
			}
			defer eng.Close()

			results, err := eng.SearchAgendas(strings.Join(args, " "), limit, deleted, branch)
			if err != nil {
				return err
			}
			if len(results) == 0 {
				fmt.Println("no results found")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tACTIVE\tPRI\tBRANCH\tTITLE\tDESCRIPTION")
			for _, a := range results {
				active := "yes"
				if !a.IsActive {
					active = "no"
				}
				desc := a.Description
				if len(desc) > 50 {
					desc = desc[:47] + "..."
				}
				fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n",
					a.ID, active, a.Priority, a.GitBranch, a.Title, desc)
			}
			w.Flush()
			return nil
		},
	}

	cmd.Flags().IntVarP(&limit, "limit", "l", 10, "Max results")
	cmd.Flags().BoolVar(&deleted, "deleted", false, "Search only soft-deleted plans")
	cmd.Flags().StringVar(&branch, "branch", "", "Filter by git branch (empty = all branches)")
	return cmd
}

// --- plan update ---

func newPlanUpdateCmd(projectDir *string) *cobra.Command {
	var title, description, guard, priority, newTasksJSON string
	var deactivate bool

	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Edit plan metadata or deactivate a plan",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid id: %w", err)
			}
			eng, err := agenda.New(*projectDir)
			if err != nil {
				return err
			}
			defer eng.Close()

			var isActive *bool
			if deactivate {
				v := false
				isActive = &v
			}

			var newTasks []agenda.TaskInput
			if newTasksJSON != "" {
				if err := json.Unmarshal([]byte(newTasksJSON), &newTasks); err != nil {
					return fmt.Errorf("--tasks JSON parse error: %w", err)
				}
			}

			if err := eng.UpdateAgenda(id, title, description, guard, priority, isActive, newTasks); err != nil {
				return err
			}
			fmt.Printf("updated plan id=%d\n", id)
			return nil
		},
	}

	cmd.Flags().StringVarP(&title, "title", "t", "", "New title")
	cmd.Flags().StringVarP(&description, "description", "d", "", "New description")
	cmd.Flags().StringVar(&guard, "guard", "", "New acceptance criteria")
	cmd.Flags().StringVarP(&priority, "priority", "p", "", "New priority: high|normal|low")
	cmd.Flags().BoolVar(&deactivate, "deactivate", false, "Mark plan as inactive")
	cmd.Flags().StringVar(&newTasksJSON, "tasks", "", `JSON array of tasks to append, e.g. '[{"Details":"..."}]'`)
	return cmd
}

// --- plan delete ---

func newPlanDeleteCmd(projectDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Soft-delete an inactive plan (recoverable with plan restore)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid id: %w", err)
			}
			eng, err := agenda.New(*projectDir)
			if err != nil {
				return err
			}
			defer eng.Close()

			if err := eng.DeleteAgenda(id); err != nil {
				return err
			}
			fmt.Printf("deleted plan id=%d (use 'plan restore %d' to undo)\n", id, id)
			return nil
		},
	}
}

// --- plan restore ---

func newPlanRestoreCmd(projectDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "restore <id>",
		Short: "Restore a soft-deleted plan",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid id: %w", err)
			}
			eng, err := agenda.New(*projectDir)
			if err != nil {
				return err
			}
			defer eng.Close()

			if err := eng.RestoreAgenda(id); err != nil {
				return err
			}
			fmt.Printf("restored plan id=%d\n", id)
			return nil
		},
	}
}

// -------------------------------------------------------------------------
// agenda task ...
// -------------------------------------------------------------------------

func newTaskCmd(projectDir *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Manage individual tasks within a plan",
	}
	cmd.AddCommand(
		newTaskAddCmd(projectDir),
		newTaskStartCmd(projectDir),
		newTaskDoneCmd(projectDir),
		newTaskReopenCmd(projectDir),
		newTaskBlockCmd(projectDir),
	)
	return cmd
}

// --- task add ---

func newTaskAddCmd(projectDir *string) *cobra.Command {
	var details string

	cmd := &cobra.Command{
		Use:   "add <plan-id>",
		Short: "Add a task to an existing plan",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			planID, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid plan id: %w", err)
			}
			if details == "" {
				return fmt.Errorf("--details is required")
			}
			eng, err := agenda.New(*projectDir)
			if err != nil {
				return err
			}
			defer eng.Close()

			taskID, err := eng.AddTask(planID, agenda.TaskInput{Details: details})
			if err != nil {
				return err
			}
			fmt.Printf("added task id=%d to plan %d\n", taskID, planID)
			return nil
		},
	}

	cmd.Flags().StringVarP(&details, "details", "T", "", "Task details (required)")
	return cmd
}

// --- task start / done / reopen / block ---

func newTaskStartCmd(projectDir *string) *cobra.Command {
	var notes string
	cmd := &cobra.Command{
		Use:   "start <plan-id> <task-number>",
		Short: "Mark a task as in-progress",
		Args:  cobra.ExactArgs(2),
		RunE:  taskStateCmd(projectDir, agenda.StatusInProgress, &notes),
	}
	cmd.Flags().StringVar(&notes, "notes", "", "Notes to attach to the task")
	return cmd
}

func newTaskDoneCmd(projectDir *string) *cobra.Command {
	var notes string
	cmd := &cobra.Command{
		Use:   "done <plan-id> <task-number>",
		Short: "Mark a task as completed",
		Args:  cobra.ExactArgs(2),
		RunE:  taskStateCmd(projectDir, agenda.StatusCompleted, &notes),
	}
	cmd.Flags().StringVar(&notes, "notes", "", "Outcome notes for this task")
	return cmd
}

func newTaskReopenCmd(projectDir *string) *cobra.Command {
	var notes string
	cmd := &cobra.Command{
		Use:   "reopen <plan-id> <task-number>",
		Short: "Mark a task as pending (also clears blocked status)",
		Args:  cobra.ExactArgs(2),
		RunE:  taskStateCmd(projectDir, agenda.StatusPending, &notes),
	}
	cmd.Flags().StringVar(&notes, "notes", "", "Notes to attach to the task")
	return cmd
}

func newTaskBlockCmd(projectDir *string) *cobra.Command {
	var notes string
	cmd := &cobra.Command{
		Use:   "block <plan-id> <task-number>",
		Short: "Mark a task as blocked (use reopen to unblock)",
		Args:  cobra.ExactArgs(2),
		RunE:  taskStateCmd(projectDir, agenda.StatusBlocked, &notes),
	}
	cmd.Flags().StringVar(&notes, "notes", "", "Reason for blocking the task")
	return cmd
}

func taskStateCmd(projectDir *string, status agenda.TaskStatus, notes *string) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		planID, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid plan id: %w", err)
		}
		taskNum, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("invalid task number: %w", err)
		}
		if taskNum < 1 {
			return fmt.Errorf("task number must be >= 1")
		}

		eng, err := agenda.New(*projectDir)
		if err != nil {
			return err
		}
		defer eng.Close()

		// Wire memory snapshot on auto-deactivation.
		wireMemoryHook(eng, *projectDir)

		n := ""
		if notes != nil {
			n = *notes
		}
		if err := eng.UpdateTaskByOrder(planID, taskNum-1, status, n); err != nil {
			return err
		}
		fmt.Printf("plan %d: task %d marked as %s\n", planID, taskNum, status)
		return nil
	}
}
