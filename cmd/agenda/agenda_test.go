package agenda_test

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	cmdagenda "context0/cmd/agenda"
)

// run executes the agenda command tree with the given args and returns stdout.
// Because the commands write directly to os.Stdout, we redirect it via a pipe.
func run(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()

	// Save and replace os.Stdout with a pipe.
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	root := &cobra.Command{Use: "context0"}
	root.AddCommand(cmdagenda.NewCmd(&dir))
	root.SetArgs(append([]string{"agenda"}, args...))
	execErr := root.Execute()

	// Flush and restore stdout.
	w.Close()
	os.Stdout = origStdout

	out, _ := io.ReadAll(r)
	r.Close()
	return string(out), execErr
}

// mustRun calls run and fails the test on error.
func mustRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := run(t, dir, args...)
	if err != nil {
		t.Fatalf("agenda %v: %v\noutput: %s", args, err, out)
	}
	return out
}

// ── plan create ────────────────────────────────────────────────────────────────

func TestPlanCreate(t *testing.T) {
	dir := t.TempDir()
	out := mustRun(t, dir, "plan", "create", "--title", "Test Plan", "--description", "A test plan")
	if !strings.Contains(out, "created plan id=") {
		t.Errorf("unexpected output: %q", out)
	}
}

func TestPlanCreateWithTasks(t *testing.T) {
	dir := t.TempDir()
	out := mustRun(t, dir,
		"plan", "create",
		"--title", "Plan With Tasks",
		"--description", "desc",
		"--task", "First task",
		"--task", "Second task",
	)
	if !strings.Contains(out, "created plan id=") {
		t.Errorf("unexpected output: %q", out)
	}
}

func TestPlanCreateWithPriority(t *testing.T) {
	dir := t.TempDir()
	out := mustRun(t, dir, "plan", "create",
		"--title", "High Priority Plan",
		"--description", "urgent",
		"--priority", "high",
	)
	if !strings.Contains(out, "created plan id=") {
		t.Errorf("unexpected output: %q", out)
	}
	// Verify priority stored correctly via get.
	getOut := mustRun(t, dir, "plan", "get", "1")
	if !strings.Contains(getOut, "high") {
		t.Errorf("priority not visible in plan get: %q", getOut)
	}
}

func TestPlanCreateInvalidPriority(t *testing.T) {
	dir := t.TempDir()
	_, err := run(t, dir, "plan", "create",
		"--title", "Bad Prio",
		"--description", "x",
		"--priority", "urgent",
	)
	if err == nil {
		t.Error("expected error for invalid priority, got nil")
	}
}

// ── plan list ─────────────────────────────────────────────────────────────────

func TestPlanListEmpty(t *testing.T) {
	dir := t.TempDir()
	out := mustRun(t, dir, "plan", "list")
	if !strings.Contains(out, "no plans found") {
		t.Errorf("expected 'no plans found', got: %q", out)
	}
}

func TestPlanListShowsCreatedPlan(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "plan", "create", "--title", "Listed Plan", "--description", "visible")
	out := mustRun(t, dir, "plan", "list")
	if !strings.Contains(out, "Listed Plan") {
		t.Errorf("plan not in list output: %q", out)
	}
}

func TestPlanListAllIncludesInactive(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "plan", "create", "--title", "Inactive Plan", "--description", "x")
	mustRun(t, dir, "plan", "update", "1", "--deactivate")
	// Active-only (default) should not show it.
	activeOut := mustRun(t, dir, "plan", "list")
	if strings.Contains(activeOut, "Inactive Plan") {
		t.Errorf("inactive plan appears in active list: %q", activeOut)
	}
	// --all should show it.
	allOut := mustRun(t, dir, "plan", "list", "--all")
	if !strings.Contains(allOut, "Inactive Plan") {
		t.Errorf("inactive plan not visible with --all: %q", allOut)
	}
}

func TestPlanListDeletedFlag(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "plan", "create", "--title", "To Delete", "--description", "x")
	mustRun(t, dir, "plan", "update", "1", "--deactivate")
	mustRun(t, dir, "plan", "delete", "1")

	// Normal list should not include it.
	out := mustRun(t, dir, "plan", "list", "--all")
	if strings.Contains(out, "To Delete") {
		t.Errorf("deleted plan appears in --all list: %q", out)
	}

	// --deleted should show it.
	deletedOut := mustRun(t, dir, "plan", "list", "--deleted")
	if !strings.Contains(deletedOut, "To Delete") {
		t.Errorf("deleted plan not visible with --deleted: %q", deletedOut)
	}
}

func TestPlanListAllAndDeletedMutuallyExclusive(t *testing.T) {
	dir := t.TempDir()
	_, err := run(t, dir, "plan", "list", "--all", "--deleted")
	if err == nil {
		t.Error("expected error for --all + --deleted combination")
	}
}

// ── plan get ──────────────────────────────────────────────────────────────────

func TestPlanGet(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "plan", "create", "--title", "Detail Plan", "--description", "detailed", "--task", "Do X")
	out := mustRun(t, dir, "plan", "get", "1")
	if !strings.Contains(out, "Detail Plan") {
		t.Errorf("plan title missing in get output: %q", out)
	}
	if !strings.Contains(out, "Do X") {
		t.Errorf("task details missing in get output: %q", out)
	}
}

func TestPlanGetNotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := run(t, dir, "plan", "get", "9999")
	if err == nil {
		t.Error("expected error for non-existent plan id, got nil")
	}
}

func TestPlanGetShowsNotes(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "plan", "create", "--title", "Notes Plan", "--description", "x", "--task", "Work item")
	mustRun(t, dir, "task", "start", "1", "1", "--notes", "started with enthusiasm")
	out := mustRun(t, dir, "plan", "get", "1")
	if !strings.Contains(out, "started with enthusiasm") {
		t.Errorf("task notes not shown in plan get: %q", out)
	}
}

// ── plan search ───────────────────────────────────────────────────────────────

func TestPlanSearch(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "plan", "create", "--title", "Searchable Plan", "--description", "unique keyword xyzzy")
	out := mustRun(t, dir, "plan", "search", "xyzzy")
	if !strings.Contains(out, "Searchable Plan") {
		t.Errorf("search did not return the plan: %q", out)
	}
}

func TestPlanSearchNoResults(t *testing.T) {
	dir := t.TempDir()
	out := mustRun(t, dir, "plan", "search", "noresultsquery")
	if !strings.Contains(out, "no results found") {
		t.Errorf("expected 'no results found': %q", out)
	}
}

// ── plan update ───────────────────────────────────────────────────────────────

func TestPlanUpdate(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "plan", "create", "--title", "Old Title", "--description", "old desc")
	mustRun(t, dir, "plan", "update", "1", "--title", "New Title")
	out := mustRun(t, dir, "plan", "get", "1")
	if !strings.Contains(out, "New Title") {
		t.Errorf("title not updated: %q", out)
	}
}

func TestPlanUpdatePriority(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "plan", "create", "--title", "Prio Plan", "--description", "x")
	mustRun(t, dir, "plan", "update", "1", "--priority", "low")
	out := mustRun(t, dir, "plan", "get", "1")
	if !strings.Contains(out, "low") {
		t.Errorf("priority not updated: %q", out)
	}
}

func TestPlanDeactivate(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "plan", "create", "--title", "Active Plan", "--description", "will deactivate")
	mustRun(t, dir, "plan", "update", "1", "--deactivate")
	// Active-only list should no longer show it.
	out := mustRun(t, dir, "plan", "list")
	if strings.Contains(out, "Active Plan") {
		t.Errorf("deactivated plan still appears in active list: %q", out)
	}
	// --all should show it.
	outAll := mustRun(t, dir, "plan", "list", "--all")
	if !strings.Contains(outAll, "Active Plan") {
		t.Errorf("deactivated plan not visible with --all: %q", outAll)
	}
}

// ── plan delete / restore ─────────────────────────────────────────────────────

func TestPlanDelete(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "plan", "create", "--title", "To Delete", "--description", "bye")
	// Must deactivate first.
	mustRun(t, dir, "plan", "update", "1", "--deactivate")
	out := mustRun(t, dir, "plan", "delete", "1")
	if !strings.Contains(out, "deleted plan id=1") {
		t.Errorf("unexpected delete output: %q", out)
	}
	// Plan should not appear in normal list.
	listOut := mustRun(t, dir, "plan", "list", "--all")
	if strings.Contains(listOut, "To Delete") {
		t.Errorf("deleted plan still appears in --all list: %q", listOut)
	}
}

func TestPlanDeleteActiveRejected(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "plan", "create", "--title", "Active Plan", "--description", "x")
	_, err := run(t, dir, "plan", "delete", "1")
	if err == nil {
		t.Error("expected error deleting active plan, got nil")
	}
}

func TestPlanRestore(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "plan", "create", "--title", "Restore Me", "--description", "x")
	mustRun(t, dir, "plan", "update", "1", "--deactivate")
	mustRun(t, dir, "plan", "delete", "1")

	// Confirm it's gone from normal list.
	before := mustRun(t, dir, "plan", "list", "--all")
	if strings.Contains(before, "Restore Me") {
		t.Errorf("deleted plan appears in --all: %q", before)
	}

	out := mustRun(t, dir, "plan", "restore", "1")
	if !strings.Contains(out, "restored plan id=1") {
		t.Errorf("unexpected restore output: %q", out)
	}

	// Should now appear in --all list.
	after := mustRun(t, dir, "plan", "list", "--all")
	if !strings.Contains(after, "Restore Me") {
		t.Errorf("restored plan not visible in --all: %q", after)
	}
}

func TestPlanRestoreNonDeletedRejected(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "plan", "create", "--title", "Not Deleted", "--description", "x")
	_, err := run(t, dir, "plan", "restore", "1")
	if err == nil {
		t.Error("expected error restoring non-deleted plan, got nil")
	}
}

// ── task add / start / done / reopen / block ─────────────────────────────────

func TestTaskLifecycle(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "plan", "create", "--title", "Task Plan", "--description", "lifecycle test")

	// Add a task.
	out := mustRun(t, dir, "task", "add", "1", "--details", "Implement feature")
	if !strings.Contains(out, "added task") {
		t.Errorf("unexpected add output: %q", out)
	}

	// Start it.
	out = mustRun(t, dir, "task", "start", "1", "1")
	if !strings.Contains(out, "in_progress") {
		t.Errorf("unexpected start output: %q", out)
	}

	// Mark done.
	out = mustRun(t, dir, "task", "done", "1", "1")
	if !strings.Contains(out, "completed") {
		t.Errorf("unexpected done output: %q", out)
	}

	// Reopen.
	out = mustRun(t, dir, "task", "reopen", "1", "1")
	if !strings.Contains(out, "pending") {
		t.Errorf("unexpected reopen output: %q", out)
	}

	// Verify via get.
	getOut := mustRun(t, dir, "plan", "get", "1")
	if !strings.Contains(getOut, "Implement feature") {
		t.Errorf("task not visible in plan get: %q", getOut)
	}
}

func TestTaskBlock(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "plan", "create", "--title", "Block Plan", "--description", "x", "--task", "Blocked work")

	out := mustRun(t, dir, "task", "block", "1", "1", "--notes", "waiting for upstream")
	if !strings.Contains(out, "blocked") {
		t.Errorf("expected 'blocked' in output: %q", out)
	}

	// Plan should still be active.
	getOut := mustRun(t, dir, "plan", "get", "1")
	if !strings.Contains(getOut, "active") {
		t.Errorf("plan should be active after blocking a task: %q", getOut)
	}

	// Notes should be stored.
	if !strings.Contains(getOut, "waiting for upstream") {
		t.Errorf("block notes not visible in plan get: %q", getOut)
	}
}

func TestTaskReopenUnblocksTask(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "plan", "create", "--title", "Unblock Plan", "--description", "x", "--task", "Stuck task")
	mustRun(t, dir, "task", "block", "1", "1")

	out := mustRun(t, dir, "task", "reopen", "1", "1")
	if !strings.Contains(out, "pending") {
		t.Errorf("expected 'pending' after reopen: %q", out)
	}
}

func TestTaskDoneWithNotes(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "plan", "create", "--title", "Notes Plan", "--description", "x", "--task", "Do work")

	mustRun(t, dir, "task", "done", "1", "1", "--notes", "finished successfully")

	out := mustRun(t, dir, "plan", "get", "1")
	if !strings.Contains(out, "finished successfully") {
		t.Errorf("done notes not visible in plan get: %q", out)
	}
}

func TestPlanSearchByTaskDetails(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "plan", "create",
		"--title", "Auth Work",
		"--description", "authentication",
		"--task", "implement clockskew tolerance in JWT validation",
	)
	out := mustRun(t, dir, "plan", "search", "clockskew")
	if !strings.Contains(out, "Auth Work") {
		t.Errorf("search by task details did not return plan: %q", out)
	}
}

func TestPlanSearchByTaskNotes(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "plan", "create",
		"--title", "Infra Work",
		"--description", "infrastructure",
		"--task", "deploy service",
	)
	mustRun(t, dir, "task", "done", "1", "1", "--notes", "used rolling deployment to avoid downtime")
	out := mustRun(t, dir, "plan", "search", "rolling")
	if !strings.Contains(out, "Infra Work") {
		t.Errorf("search by task notes did not return plan: %q", out)
	}
}

func TestTaskAddRequiresDetails(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "plan", "create", "--title", "Plan", "--description", "")
	_, err := run(t, dir, "task", "add", "1")
	if err == nil {
		t.Error("expected error when --details is missing")
	}
}
