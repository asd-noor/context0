package agenda

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// TaskStatus represents the lifecycle state of a task.
type TaskStatus string

const (
	// StatusPending is the initial state of every new task.
	StatusPending TaskStatus = "pending"
	// StatusInProgress indicates the task is actively being worked on.
	StatusInProgress TaskStatus = "in_progress"
	// StatusCompleted indicates the task has been finished.
	StatusCompleted TaskStatus = "completed"
	// StatusBlocked indicates the task cannot proceed due to an external
	// dependency. Use UpdateTaskByOrder with StatusPending to unblock.
	StatusBlocked TaskStatus = "blocked"
)

// IsValid reports whether s is a recognised task status.
func (s TaskStatus) IsValid() bool {
	switch s {
	case StatusPending, StatusInProgress, StatusCompleted, StatusBlocked:
		return true
	}
	return false
}

// Priority levels for agendas.
const (
	PriorityHigh   = "high"
	PriorityNormal = "normal"
	PriorityLow    = "low"
)

// ValidPriority reports whether p is one of the recognised priority strings.
func ValidPriority(p string) bool {
	return p == PriorityHigh || p == PriorityNormal || p == PriorityLow
}

// priorityRank returns a sort key so high < normal < low.
const priorityOrderExpr = `CASE priority WHEN 'high' THEN 0 WHEN 'normal' THEN 1 ELSE 2 END`

// Agenda represents a named plan with an ordered list of tasks.
type Agenda struct {
	ID              int64      `json:"id"`
	IsActive        bool       `json:"is_active"`
	GitBranch       string     `json:"git_branch,omitempty"`
	Priority        string     `json:"priority"`
	Title           string     `json:"title"`
	Description     string     `json:"description"`
	AcceptanceGuard string     `json:"acceptance_guard,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	DeletedAt       *time.Time `json:"deleted_at,omitempty"`
	Tasks           []Task     `json:"tasks,omitempty"`
}

// Task is a single step within an Agenda.
type Task struct {
	ID       int64      `json:"id"`
	AgendaID int64      `json:"agenda_id"`
	Details  string     `json:"details"`
	Notes    string     `json:"notes,omitempty"`
	Status   TaskStatus `json:"status"`
}

// TaskInput is the DTO used to create a new task.
type TaskInput struct {
	Details string
}

// Engine wraps the agenda database and exposes CRUD operations.
type Engine struct {
	db      *sql.DB
	onClose func(Agenda)
}

// SetOnClose registers a hook that is called after an agenda is automatically
// deactivated (all tasks completed). The hook receives the full, freshly-loaded
// agenda. Errors inside the hook are the caller's responsibility.
func (e *Engine) SetOnClose(fn func(Agenda)) {
	e.onClose = fn
}

// New opens the agenda database for projectPath and returns an Engine.
func New(projectPath string) (*Engine, error) {
	db, err := Open(projectPath)
	if err != nil {
		return nil, err
	}
	return &Engine{db: db}, nil
}

// Close releases the underlying database connection.
func (e *Engine) Close() error {
	return e.db.Close()
}

// CreateAgenda inserts a new agenda with the supplied tasks.
// priority must be one of PriorityHigh / PriorityNormal / PriorityLow;
// if empty, PriorityNormal is used.
// gitBranch records the VCS branch the agenda was created on (may be empty).
// Returns the agenda_id.
func (e *Engine) CreateAgenda(title, description, acceptanceGuard, gitBranch, priority string, tasks []TaskInput) (int64, error) {
	if priority == "" {
		priority = PriorityNormal
	}
	if !ValidPriority(priority) {
		return 0, fmt.Errorf("create_agenda: invalid priority %q (want high|normal|low)", priority)
	}

	tx, err := e.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("create_agenda: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	res, err := tx.Exec(
		`INSERT INTO agendas (title, description, acceptance_guard, git_branch, priority)
		 VALUES (?, ?, ?, ?, ?)`,
		title, description, acceptanceGuard, gitBranch, priority,
	)
	if err != nil {
		return 0, fmt.Errorf("create_agenda: insert agenda: %w", err)
	}
	agendaID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("create_agenda: last insert id: %w", err)
	}

	for i, t := range tasks {
		if _, err := tx.Exec(
			`INSERT INTO tasks (agenda_id, details, status) VALUES (?, ?, ?)`,
			agendaID, t.Details, string(StatusPending),
		); err != nil {
			return 0, fmt.Errorf("create_agenda: insert task %d: %w", i, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("create_agenda: commit: %w", err)
	}
	return agendaID, nil
}

// ListAgendas returns agendas ordered by priority (high → normal → low) then
// by id descending.
//
//   - activeOnly: if true, only active (is_active=1) agendas are returned.
//     Ignored when showDeleted is true.
//   - showDeleted: if true, returns ONLY soft-deleted agendas (deleted_at IS
//     NOT NULL). Mutually exclusive with the normal view.
//   - gitBranch: if non-empty, narrows results to that branch.
func (e *Engine) ListAgendas(activeOnly bool, showDeleted bool, gitBranch string) ([]Agenda, error) {
	args := []interface{}{}

	var q string
	if showDeleted {
		q = `SELECT id, is_active, git_branch, priority, title, description, created_at, completed_at, deleted_at
		     FROM agendas WHERE deleted_at IS NOT NULL`
	} else {
		q = `SELECT id, is_active, git_branch, priority, title, description, created_at, completed_at, deleted_at
		     FROM agendas WHERE deleted_at IS NULL`
		if activeOnly {
			q += ` AND is_active=1`
		}
	}
	if gitBranch != "" {
		q += ` AND git_branch=?`
		args = append(args, gitBranch)
	}
	q += ` ORDER BY ` + priorityOrderExpr + ` ASC, id DESC`

	rows, err := e.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("list_agendas: query: %w", err)
	}
	defer rows.Close()

	var agendas []Agenda
	for rows.Next() {
		var a Agenda
		var isActive int
		var completedAt, deletedAt sql.NullTime
		if err := rows.Scan(
			&a.ID, &isActive, &a.GitBranch, &a.Priority,
			&a.Title, &a.Description,
			&a.CreatedAt, &completedAt, &deletedAt,
		); err != nil {
			return nil, fmt.Errorf("list_agendas: scan: %w", err)
		}
		a.IsActive = isActive == 1
		if completedAt.Valid {
			t := completedAt.Time
			a.CompletedAt = &t
		}
		if deletedAt.Valid {
			t := deletedAt.Time
			a.DeletedAt = &t
		}
		agendas = append(agendas, a)
	}
	return agendas, rows.Err()
}

// GetAgenda returns the full agenda with all tasks ordered by insertion.
func (e *Engine) GetAgenda(id int64) (*Agenda, error) {
	a, err := e.getAgendaRow(id)
	if err != nil {
		return nil, err
	}
	tasks, err := e.getTasksForAgenda(id)
	if err != nil {
		return nil, fmt.Errorf("get_agenda: fetch tasks: %w", err)
	}
	a.Tasks = tasks
	return a, nil
}

// SearchAgendas performs FTS5 search across agenda fields (title, description,
// acceptance_guard) and task fields (details, notes). Deleted agendas are
// excluded unless showDeleted is true. If gitBranch is non-empty, results are
// narrowed to that branch.
func (e *Engine) SearchAgendas(query string, limit int, showDeleted bool, gitBranch string) ([]Agenda, error) {
	if limit <= 0 {
		limit = 10
	}

	safe := fts5Escape(query)

	deletedFilter := `AND a.deleted_at IS NULL`
	if showDeleted {
		deletedFilter = `AND a.deleted_at IS NOT NULL`
	}

	// Match against agenda fields OR task fields; UNION deduplicates agenda IDs.
	// tasks_fts is a content table (content='tasks'), so agenda_id must be
	// resolved via the tasks table using the rowid returned by the FTS query.
	q := `SELECT a.id, a.is_active, a.git_branch, a.priority, a.title, a.description,
	             a.created_at, a.completed_at, a.deleted_at
	      FROM agendas a
	      WHERE a.id IN (
	          SELECT rowid FROM agendas_fts WHERE agendas_fts MATCH ?
	          UNION
	          SELECT agenda_id FROM tasks WHERE id IN (SELECT rowid FROM tasks_fts WHERE tasks_fts MATCH ?)
	      )
	      ` + deletedFilter

	args := []interface{}{safe, safe}

	if gitBranch != "" {
		q += ` AND a.git_branch=?`
		args = append(args, gitBranch)
	}
	q += ` ORDER BY ` + priorityOrderExpr + ` ASC, a.id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := e.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agendas []Agenda
	for rows.Next() {
		var a Agenda
		var isActive int
		var completedAt, deletedAt sql.NullTime
		if err := rows.Scan(
			&a.ID, &isActive, &a.GitBranch, &a.Priority,
			&a.Title, &a.Description,
			&a.CreatedAt, &completedAt, &deletedAt,
		); err != nil {
			return nil, fmt.Errorf("search_agendas: scan: %w", err)
		}
		a.IsActive = isActive == 1
		if completedAt.Valid {
			t := completedAt.Time
			a.CompletedAt = &t
		}
		if deletedAt.Valid {
			t := deletedAt.Time
			a.DeletedAt = &t
		}
		agendas = append(agendas, a)
	}
	return agendas, rows.Err()
}

// UpdateTaskByOrder sets the status (and optionally notes) for a task
// identified by agenda ID and 0-based insertion order, then runs
// auto-deactivation.
//
// notes is written only when non-empty (pass "" to leave existing notes
// unchanged). status must be one of the StatusXxx constants.
func (e *Engine) UpdateTaskByOrder(agendaID int64, taskOrder int, status TaskStatus, notes string) error {
	if !status.IsValid() {
		return fmt.Errorf("update_task: invalid status %q", status)
	}
	var taskID int64
	err := e.db.QueryRow(
		`SELECT id FROM tasks WHERE agenda_id=? ORDER BY id LIMIT 1 OFFSET ?`,
		agendaID, taskOrder,
	).Scan(&taskID)
	if err != nil {
		return fmt.Errorf("update_task: no task at order %d in agenda %d: %w", taskOrder, agendaID, err)
	}
	return e.UpdateTask(taskID, status, notes)
}

// UpdateTask sets the status (and optionally notes) for a task and runs
// auto-deactivation. notes is written only when non-empty.
func (e *Engine) UpdateTask(taskID int64, status TaskStatus, notes string) error {
	if !status.IsValid() {
		return fmt.Errorf("update_task: invalid status %q", status)
	}

	tx, err := e.db.Begin()
	if err != nil {
		return fmt.Errorf("update_task: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var agendaID int64
	if err := tx.QueryRow(`SELECT agenda_id FROM tasks WHERE id=?`, taskID).Scan(&agendaID); err != nil {
		return fmt.Errorf("update_task: find task: %w", err)
	}

	if notes != "" {
		if _, err := tx.Exec(
			`UPDATE tasks SET status=?, notes=? WHERE id=?`,
			string(status), notes, taskID,
		); err != nil {
			return fmt.Errorf("update_task: update with notes: %w", err)
		}
	} else {
		if _, err := tx.Exec(
			`UPDATE tasks SET status=? WHERE id=?`,
			string(status), taskID,
		); err != nil {
			return fmt.Errorf("update_task: update: %w", err)
		}
	}

	// Auto-deactivation: close the agenda when every task is completed.
	// Blocked tasks are not completed — they keep the agenda open.
	var incomplete int
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM tasks WHERE agenda_id=? AND status != 'completed'`,
		agendaID,
	).Scan(&incomplete); err != nil {
		return fmt.Errorf("update_task: check completion: %w", err)
	}

	deactivated := false
	if incomplete == 0 {
		if _, err := tx.Exec(
			`UPDATE agendas SET is_active=0, completed_at=CURRENT_TIMESTAMP WHERE id=?`,
			agendaID,
		); err != nil {
			return fmt.Errorf("update_task: deactivate agenda: %w", err)
		}
		deactivated = true
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("update_task: commit: %w", err)
	}

	// Fire the onClose hook after the transaction is committed so the hook
	// sees the final state. Errors in the hook are silently ignored here —
	// the caller registered the hook and owns any error handling inside it.
	if deactivated && e.onClose != nil {
		if a, err := e.GetAgenda(agendaID); err == nil {
			e.onClose(*a)
		}
	}
	return nil
}

// UpdateAgenda updates agenda metadata and optionally appends new tasks.
// Pass empty strings for fields that should not change. priority="" is a no-op.
// Pass nil for newTasks to skip task appending.
// If isActive is set to false, completed_at is also recorded.
func (e *Engine) UpdateAgenda(id int64, title, description, acceptanceGuard, priority string, isActive *bool, newTasks []TaskInput) error {
	if priority != "" && !ValidPriority(priority) {
		return fmt.Errorf("update_agenda: invalid priority %q (want high|normal|low)", priority)
	}

	tx, err := e.db.Begin()
	if err != nil {
		return fmt.Errorf("update_agenda: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	setClauses := []string{}
	args := []interface{}{}

	if title != "" {
		setClauses = append(setClauses, "title=?")
		args = append(args, title)
	}
	if description != "" {
		setClauses = append(setClauses, "description=?")
		args = append(args, description)
	}
	if acceptanceGuard != "" {
		setClauses = append(setClauses, "acceptance_guard=?")
		args = append(args, acceptanceGuard)
	}
	if priority != "" {
		setClauses = append(setClauses, "priority=?")
		args = append(args, priority)
	}
	if isActive != nil {
		setClauses = append(setClauses, "is_active=?")
		args = append(args, boolToInt(*isActive))
		if !*isActive {
			// Record closure time on manual deactivation too.
			setClauses = append(setClauses, "completed_at=CURRENT_TIMESTAMP")
		}
	}

	if len(setClauses) > 0 {
		args = append(args, id)
		q := "UPDATE agendas SET " + strings.Join(setClauses, ", ") + " WHERE id=?"
		if _, err := tx.Exec(q, args...); err != nil {
			return fmt.Errorf("update_agenda: update metadata: %w", err)
		}
	}

	for i, t := range newTasks {
		if _, err := tx.Exec(
			`INSERT INTO tasks (agenda_id, details, status) VALUES (?, ?, ?)`,
			id, t.Details, string(StatusPending),
		); err != nil {
			return fmt.Errorf("update_agenda: insert new task %d: %w", i, err)
		}
	}

	return tx.Commit()
}

// DeleteAgenda soft-deletes an agenda by setting deleted_at.
// The agenda must be inactive first.
func (e *Engine) DeleteAgenda(id int64) error {
	var isActive int
	var deletedAt sql.NullTime
	if err := e.db.QueryRow(
		`SELECT is_active, deleted_at FROM agendas WHERE id=?`, id,
	).Scan(&isActive, &deletedAt); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("delete_agenda: agenda %d not found", id)
		}
		return fmt.Errorf("delete_agenda: check: %w", err)
	}
	if isActive == 1 {
		return fmt.Errorf("delete_agenda: agenda %d is still active; deactivate it first", id)
	}
	if deletedAt.Valid {
		return fmt.Errorf("delete_agenda: agenda %d is already deleted", id)
	}

	if _, err := e.db.Exec(
		`UPDATE agendas SET deleted_at=CURRENT_TIMESTAMP WHERE id=?`, id,
	); err != nil {
		return fmt.Errorf("delete_agenda: soft-delete: %w", err)
	}
	return nil
}

// RestoreAgenda clears the deleted_at timestamp, making the agenda visible
// again in normal queries.
func (e *Engine) RestoreAgenda(id int64) error {
	var deletedAt sql.NullTime
	if err := e.db.QueryRow(
		`SELECT deleted_at FROM agendas WHERE id=?`, id,
	).Scan(&deletedAt); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("restore_agenda: agenda %d not found", id)
		}
		return fmt.Errorf("restore_agenda: check: %w", err)
	}
	if !deletedAt.Valid {
		return fmt.Errorf("restore_agenda: agenda %d is not deleted", id)
	}

	if _, err := e.db.Exec(
		`UPDATE agendas SET deleted_at=NULL WHERE id=?`, id,
	); err != nil {
		return fmt.Errorf("restore_agenda: restore: %w", err)
	}
	return nil
}

// AddTask appends a single task to an existing agenda and returns the new task ID.
func (e *Engine) AddTask(agendaID int64, task TaskInput) (int64, error) {
	var count int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM agendas WHERE id=?`, agendaID).Scan(&count); err != nil {
		return 0, fmt.Errorf("add_task: check agenda: %w", err)
	}
	if count == 0 {
		return 0, fmt.Errorf("add_task: plan %d not found", agendaID)
	}

	res, err := e.db.Exec(
		`INSERT INTO tasks (agenda_id, details, status) VALUES (?, ?, ?)`,
		agendaID, task.Details, string(StatusPending),
	)
	if err != nil {
		return 0, fmt.Errorf("add_task: insert: %w", err)
	}
	taskID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("add_task: last insert id: %w", err)
	}
	return taskID, nil
}

// --- internal helpers ---

func (e *Engine) getAgendaRow(id int64) (*Agenda, error) {
	var a Agenda
	var isActive int
	var completedAt, deletedAt sql.NullTime
	err := e.db.QueryRow(
		`SELECT id, is_active, git_branch, priority, title, description, acceptance_guard,
		        created_at, completed_at, deleted_at
		 FROM agendas WHERE id=?`, id,
	).Scan(
		&a.ID, &isActive, &a.GitBranch, &a.Priority,
		&a.Title, &a.Description, &a.AcceptanceGuard,
		&a.CreatedAt, &completedAt, &deletedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get_agenda: not found: %w", err)
	}
	a.IsActive = isActive == 1
	if completedAt.Valid {
		t := completedAt.Time
		a.CompletedAt = &t
	}
	if deletedAt.Valid {
		t := deletedAt.Time
		a.DeletedAt = &t
	}
	return &a, nil
}

func (e *Engine) getTasksForAgenda(agendaID int64) ([]Task, error) {
	rows, err := e.db.Query(
		`SELECT id, agenda_id, details, notes, status FROM tasks WHERE agenda_id=? ORDER BY id`,
		agendaID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		var statusStr string
		if err := rows.Scan(&t.ID, &t.AgendaID, &t.Details, &t.Notes, &statusStr); err != nil {
			return nil, err
		}
		t.Status = TaskStatus(statusStr)
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func fts5Escape(query string) string {
	tokens := strings.Fields(query)
	quoted := make([]string, 0, len(tokens))
	for _, t := range tokens {
		t = strings.ReplaceAll(t, `"`, `""`)
		quoted = append(quoted, `"`+t+`"`)
	}
	return strings.Join(quoted, " ")
}
