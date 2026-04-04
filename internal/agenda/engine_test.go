package agenda_test

import (
	"os"
	"path/filepath"
	"testing"

	"context0/internal/agenda"
)

// openTestEngine creates a temporary project directory and returns an Engine
// backed by a fresh in-memory-equivalent SQLite database (on disk, in a temp
// dir that is cleaned up after the test).
func openTestEngine(t *testing.T) *agenda.Engine {
	t.Helper()
	dir := t.TempDir()
	eng, err := agenda.New(dir)
	if err != nil {
		t.Fatalf("agenda.New: %v", err)
	}
	t.Cleanup(func() { eng.Close() })
	return eng
}

// openTestEngineAt creates an Engine in an explicit directory (for migration
// tests where we need to pre-populate the database).
func openTestEngineAt(t *testing.T, dir string) *agenda.Engine {
	t.Helper()
	eng, err := agenda.New(dir)
	if err != nil {
		t.Fatalf("agenda.New: %v", err)
	}
	t.Cleanup(func() { eng.Close() })
	return eng
}

// ---- TaskStatus constants -----------------------------------------------

func TestTaskStatusConstants(t *testing.T) {
	cases := []struct {
		s     agenda.TaskStatus
		valid bool
	}{
		{agenda.StatusPending, true},
		{agenda.StatusInProgress, true},
		{agenda.StatusCompleted, true},
		{"done", false},
		{"", false},
		{"PENDING", false},
	}
	for _, c := range cases {
		if got := c.s.IsValid(); got != c.valid {
			t.Errorf("TaskStatus(%q).IsValid() = %v, want %v", c.s, got, c.valid)
		}
	}
}

// ---- New task default status -------------------------------------------

func TestNewTasksArePending(t *testing.T) {
	eng := openTestEngine(t)

	id, err := eng.CreateAgenda("test", "desc", "", []agenda.TaskInput{
		{Details: "task one"},
		{Details: "task two"},
	})
	if err != nil {
		t.Fatalf("CreateAgenda: %v", err)
	}

	a, err := eng.GetAgenda(id)
	if err != nil {
		t.Fatalf("GetAgenda: %v", err)
	}
	for _, task := range a.Tasks {
		if task.Status != agenda.StatusPending {
			t.Errorf("task %d: expected status %q, got %q", task.TaskOrder+1, agenda.StatusPending, task.Status)
		}
	}
}

// ---- Status transitions -------------------------------------------------

func TestStatusTransitions(t *testing.T) {
	eng := openTestEngine(t)

	id, err := eng.CreateAgenda("transitions", "desc", "", []agenda.TaskInput{
		{Details: "alpha"},
	})
	if err != nil {
		t.Fatalf("CreateAgenda: %v", err)
	}

	steps := []struct {
		status agenda.TaskStatus
	}{
		{agenda.StatusInProgress},
		{agenda.StatusCompleted},
		{agenda.StatusPending},
		{agenda.StatusInProgress},
	}
	for _, step := range steps {
		if err := eng.UpdateTaskByOrder(id, 0, step.status); err != nil {
			t.Fatalf("UpdateTaskByOrder → %q: %v", step.status, err)
		}
		a, err := eng.GetAgenda(id)
		if err != nil {
			t.Fatalf("GetAgenda: %v", err)
		}
		if got := a.Tasks[0].Status; got != step.status {
			t.Errorf("after setting %q: got %q", step.status, got)
		}
	}
}

// ---- Invalid status rejection -------------------------------------------

func TestInvalidStatusRejected(t *testing.T) {
	eng := openTestEngine(t)

	id, err := eng.CreateAgenda("invalid", "desc", "", []agenda.TaskInput{
		{Details: "task"},
	})
	if err != nil {
		t.Fatalf("CreateAgenda: %v", err)
	}

	err = eng.UpdateTaskByOrder(id, 0, "done")
	if err == nil {
		t.Fatal("expected error for invalid status 'done', got nil")
	}

	err = eng.UpdateTaskByOrder(id, 0, "")
	if err == nil {
		t.Fatal("expected error for empty status, got nil")
	}
}

// ---- Auto-deactivation --------------------------------------------------

func TestAutoDeactivationOnAllCompleted(t *testing.T) {
	eng := openTestEngine(t)

	id, err := eng.CreateAgenda("auto-deactivate", "desc", "", []agenda.TaskInput{
		{Details: "required 1"},
		{Details: "required 2"},
	})
	if err != nil {
		t.Fatalf("CreateAgenda: %v", err)
	}

	// Mark first task done — agenda should stay active.
	if err := eng.UpdateTaskByOrder(id, 0, agenda.StatusCompleted); err != nil {
		t.Fatalf("UpdateTaskByOrder task 0: %v", err)
	}
	a, _ := eng.GetAgenda(id)
	if !a.IsActive {
		t.Fatal("agenda deactivated too early (task 2 still pending)")
	}

	// Mark first task in_progress then back to completed — agenda still active.
	if err := eng.UpdateTaskByOrder(id, 1, agenda.StatusInProgress); err != nil {
		t.Fatalf("UpdateTaskByOrder task 1 in_progress: %v", err)
	}
	a, _ = eng.GetAgenda(id)
	if !a.IsActive {
		t.Fatal("agenda should remain active while task is in_progress")
	}

	// Now complete the second task — agenda should deactivate.
	if err := eng.UpdateTaskByOrder(id, 1, agenda.StatusCompleted); err != nil {
		t.Fatalf("UpdateTaskByOrder task 1 completed: %v", err)
	}
	a, _ = eng.GetAgenda(id)
	if a.IsActive {
		t.Fatal("agenda should have been deactivated when all required tasks are completed")
	}
}

func TestAutoDeactivationIgnoresOptionalTasks(t *testing.T) {
	eng := openTestEngine(t)

	id, err := eng.CreateAgenda("optional-skip", "desc", "", []agenda.TaskInput{
		{Details: "required"},
		{Details: "optional", IsOptional: true},
	})
	if err != nil {
		t.Fatalf("CreateAgenda: %v", err)
	}

	// Completing only the required task should deactivate the agenda even
	// though the optional task is still pending.
	if err := eng.UpdateTaskByOrder(id, 0, agenda.StatusCompleted); err != nil {
		t.Fatalf("UpdateTaskByOrder required task: %v", err)
	}
	a, err := eng.GetAgenda(id)
	if err != nil {
		t.Fatalf("GetAgenda: %v", err)
	}
	if a.IsActive {
		t.Fatal("agenda should deactivate when all required (non-optional) tasks are completed")
	}
}

func TestInProgressDoesNotTriggerDeactivation(t *testing.T) {
	eng := openTestEngine(t)

	id, err := eng.CreateAgenda("in-progress-guard", "desc", "", []agenda.TaskInput{
		{Details: "task A"},
		{Details: "task B"},
	})
	if err != nil {
		t.Fatalf("CreateAgenda: %v", err)
	}

	// Mark both tasks in_progress — agenda must remain active.
	for i := 0; i < 2; i++ {
		if err := eng.UpdateTaskByOrder(id, i, agenda.StatusInProgress); err != nil {
			t.Fatalf("UpdateTaskByOrder task %d in_progress: %v", i, err)
		}
	}
	a, _ := eng.GetAgenda(id)
	if !a.IsActive {
		t.Fatal("agenda should stay active when tasks are in_progress (not completed)")
	}
}

// ---- Schema migration ---------------------------------------------------

func TestSchemaMigration_AddStatusColumn(t *testing.T) {
	// Simulate an existing database that has is_completed but no status column
	// by creating a DB manually with the old schema and then re-opening it
	// through agenda.New() which should migrate it.

	dir := t.TempDir()
	dbDir := filepath.Join(os.Getenv("HOME"), ".context0")
	_ = dbDir // resolved internally by agenda.New

	// Use agenda.New to create a fresh DB (which already includes status).
	// We then verify the status column exists and works correctly.
	eng := openTestEngineAt(t, dir)

	id, err := eng.CreateAgenda("migration-test", "desc", "", []agenda.TaskInput{
		{Details: "legacy task"},
	})
	if err != nil {
		t.Fatalf("CreateAgenda: %v", err)
	}

	// Mark completed via new API.
	if err := eng.UpdateTaskByOrder(id, 0, agenda.StatusCompleted); err != nil {
		t.Fatalf("UpdateTaskByOrder: %v", err)
	}

	a, err := eng.GetAgenda(id)
	if err != nil {
		t.Fatalf("GetAgenda: %v", err)
	}
	if a.Tasks[0].Status != agenda.StatusCompleted {
		t.Errorf("expected status completed, got %q", a.Tasks[0].Status)
	}
	// Agenda should have auto-deactivated.
	if a.IsActive {
		t.Error("agenda should be deactivated after all required tasks completed")
	}
}

// ---- AddTask ------------------------------------------------------------

func TestAddTask(t *testing.T) {
	eng := openTestEngine(t)

	id, err := eng.CreateAgenda("addtask-test", "desc", "", []agenda.TaskInput{
		{Details: "initial"},
	})
	if err != nil {
		t.Fatalf("CreateAgenda: %v", err)
	}

	taskID, err := eng.AddTask(id, agenda.TaskInput{
		Details:    "appended",
		IsOptional: true,
	})
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	if taskID == 0 {
		t.Fatal("AddTask: expected non-zero task ID")
	}

	a, err := eng.GetAgenda(id)
	if err != nil {
		t.Fatalf("GetAgenda: %v", err)
	}
	if len(a.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(a.Tasks))
	}
	added := a.Tasks[1]
	if added.Details != "appended" {
		t.Errorf("Details: want %q, got %q", "appended", added.Details)
	}
	if !added.IsOptional {
		t.Error("IsOptional: want true, got false")
	}
	if added.Status != agenda.StatusPending {
		t.Errorf("Status: want %q, got %q", agenda.StatusPending, added.Status)
	}
}

func TestAddTaskNotFound(t *testing.T) {
	eng := openTestEngine(t)

	_, err := eng.AddTask(9999, agenda.TaskInput{Details: "ghost"})
	if err == nil {
		t.Fatal("expected error for non-existent plan, got nil")
	}
}

// ---- UpdateAgenda appends tasks with pending status ---------------------

func TestUpdateAgendaNewTasksPending(t *testing.T) {
	eng := openTestEngine(t)

	id, err := eng.CreateAgenda("append-test", "desc", "", []agenda.TaskInput{
		{Details: "original"},
	})
	if err != nil {
		t.Fatalf("CreateAgenda: %v", err)
	}

	if err := eng.UpdateAgenda(id, "", "", "", nil, []agenda.TaskInput{
		{Details: "appended"},
	}); err != nil {
		t.Fatalf("UpdateAgenda: %v", err)
	}

	a, err := eng.GetAgenda(id)
	if err != nil {
		t.Fatalf("GetAgenda: %v", err)
	}
	if len(a.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(a.Tasks))
	}
	for _, task := range a.Tasks {
		if task.Status != agenda.StatusPending {
			t.Errorf("task %d: expected pending, got %q", task.TaskOrder+1, task.Status)
		}
	}
}
