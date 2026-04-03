// Package agenda provides the `context0 agenda` CLI sub-commands.
package agenda

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"context0/internal/agenda"
)

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
	)
	return cmd
}

// --- plan list ---

func newPlanListCmd(projectDir *string) *cobra.Command {
	var all bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List plans (active only by default)",
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := agenda.New(*projectDir)
			if err != nil {
				return err
			}
			defer eng.Close()

			plans, err := eng.ListAgendas(!all)
			if err != nil {
				return err
			}
			if len(plans) == 0 {
				fmt.Println("no plans found")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tACTIVE\tTITLE\tDESCRIPTION")
			for _, a := range plans {
				active := "yes"
				if !a.IsActive {
					active = "no"
				}
				desc := a.Description
				if len(desc) > 60 {
					desc = desc[:57] + "..."
				}
				fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", a.ID, active, a.Title, desc)
			}
			w.Flush()
			return nil
		},
	}

	cmd.Flags().BoolVar(&all, "all", false, "Include inactive plans")
	return cmd
}

// --- plan get ---

// taskStatusSymbol returns the display symbol for a task's status.
//
//	pending     → [ ]
//	in_progress → [→]
//	completed   → [x]
func taskStatusSymbol(s agenda.TaskStatus) string {
	switch s {
	case agenda.StatusInProgress:
		return "[→]"
	case agenda.StatusCompleted:
		return "[x]"
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

			active := "active"
			if !a.IsActive {
				active = "inactive"
			}
			fmt.Printf("Plan #%d [%s]\n", a.ID, active)
			fmt.Printf("  Title      : %s\n", a.Title)
			fmt.Printf("  Description: %s\n", a.Description)
			fmt.Printf("  Created    : %s\n", a.CreatedAt.Format("2006-01-02 15:04:05"))
			fmt.Printf("  Tasks (%d):\n", len(a.Tasks))
			for _, t := range a.Tasks {
				symbol := taskStatusSymbol(t.Status)
				opt := ""
				if t.IsOptional {
					opt = " (optional)"
				}
				fmt.Printf("    %s #%d%s: %s\n", symbol, t.TaskOrder+1, opt, t.Details)
				if t.AcceptanceGuard != "" {
					fmt.Printf("         Done when: %s\n", t.AcceptanceGuard)
				}
			}
			return nil
		},
	}
}

// --- plan create ---

func newPlanCreateCmd(projectDir *string) *cobra.Command {
	var title, description string
	var taskDetails []string
	var taskOptional []bool
	var taskGuards []string

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new plan with an optional initial task list",
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := agenda.New(*projectDir)
			if err != nil {
				return err
			}
			defer eng.Close()

			tasks := make([]agenda.TaskInput, len(taskDetails))
			for i, d := range taskDetails {
				optional := false
				if i < len(taskOptional) {
					optional = taskOptional[i]
				}
				guard := ""
				if i < len(taskGuards) {
					guard = taskGuards[i]
				}
				tasks[i] = agenda.TaskInput{Details: d, IsOptional: optional, AcceptanceGuard: guard}
			}

			id, err := eng.CreateAgenda(title, description, tasks)
			if err != nil {
				return err
			}
			fmt.Printf("created plan id=%d\n", id)
			return nil
		},
	}

	cmd.Flags().StringVarP(&title, "title", "t", "", "Plan title")
	cmd.Flags().StringVarP(&description, "description", "d", "", "Plan description")
	cmd.Flags().StringArrayVarP(&taskDetails, "task", "T", nil, "Task details (repeat for multiple tasks)")
	cmd.Flags().BoolSliceVar(&taskOptional, "task-optional", nil, "Mark corresponding task as optional (repeat to match --task order)")
	cmd.Flags().StringArrayVar(&taskGuards, "task-guard", nil, "Acceptance criteria for corresponding task (repeat to match --task order)")
	return cmd
}

// --- plan search ---

func newPlanSearchCmd(projectDir *string) *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Full-text search across plan titles and descriptions",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eng, err := agenda.New(*projectDir)
			if err != nil {
				return err
			}
			defer eng.Close()

			results, err := eng.SearchAgendas(strings.Join(args, " "), limit)
			if err != nil {
				return err
			}
			if len(results) == 0 {
				fmt.Println("no results found")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tACTIVE\tTITLE\tDESCRIPTION")
			for _, a := range results {
				active := "yes"
				if !a.IsActive {
					active = "no"
				}
				desc := a.Description
				if len(desc) > 60 {
					desc = desc[:57] + "..."
				}
				fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", a.ID, active, a.Title, desc)
			}
			w.Flush()
			return nil
		},
	}

	cmd.Flags().IntVarP(&limit, "limit", "l", 10, "Max results")
	return cmd
}

// --- plan update ---

func newPlanUpdateCmd(projectDir *string) *cobra.Command {
	var title, description, newTasksJSON string
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

			if err := eng.UpdateAgenda(id, title, description, isActive, newTasks); err != nil {
				return err
			}
			fmt.Printf("updated plan id=%d\n", id)
			return nil
		},
	}

	cmd.Flags().StringVarP(&title, "title", "t", "", "New title")
	cmd.Flags().StringVarP(&description, "description", "d", "", "New description")
	cmd.Flags().BoolVar(&deactivate, "deactivate", false, "Mark plan as inactive")
	cmd.Flags().StringVar(&newTasksJSON, "tasks", "", `JSON array of tasks to append, e.g. '[{"Details":"...","IsOptional":false}]'`)
	return cmd
}

// --- plan delete ---

func newPlanDeleteCmd(projectDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete an inactive plan",
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
			fmt.Printf("deleted plan id=%d\n", id)
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
	)
	return cmd
}

// --- task add ---

func newTaskAddCmd(projectDir *string) *cobra.Command {
	var details string
	var optional bool
	var guard string

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

			taskID, err := eng.AddTask(planID, agenda.TaskInput{
				Details:         details,
				IsOptional:      optional,
				AcceptanceGuard: guard,
			})
			if err != nil {
				return err
			}
			fmt.Printf("added task id=%d to plan %d\n", taskID, planID)
			return nil
		},
	}

	cmd.Flags().StringVarP(&details, "details", "T", "", "Task details (required)")
	cmd.Flags().BoolVar(&optional, "optional", false, "Mark task as optional")
	cmd.Flags().StringVar(&guard, "guard", "", "Acceptance criteria (done-when condition)")
	return cmd
}

// --- task start / done / reopen ---

func newTaskStartCmd(projectDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "start <plan-id> <task-number>",
		Short: "Mark a task as in-progress",
		Args:  cobra.ExactArgs(2),
		RunE:  taskStateCmd(projectDir, agenda.StatusInProgress),
	}
}

func newTaskDoneCmd(projectDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "done <plan-id> <task-number>",
		Short: "Mark a task as completed",
		Args:  cobra.ExactArgs(2),
		RunE:  taskStateCmd(projectDir, agenda.StatusCompleted),
	}
}

func newTaskReopenCmd(projectDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "reopen <plan-id> <task-number>",
		Short: "Mark a task as pending",
		Args:  cobra.ExactArgs(2),
		RunE:  taskStateCmd(projectDir, agenda.StatusPending),
	}
}

func taskStateCmd(projectDir *string, status agenda.TaskStatus) func(cmd *cobra.Command, args []string) error {
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

		if err := eng.UpdateTaskByOrder(planID, taskNum-1, status); err != nil {
			return err
		}
		fmt.Printf("plan %d: task %d marked as %s\n", planID, taskNum, status)
		return nil
	}
}
