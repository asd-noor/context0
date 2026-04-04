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
	// StatusPending is the initial state of every task.
	StatusPending TaskStatus = "pending"
	// StatusInProgress indicates the task is actively being worked on.
	StatusInProgress TaskStatus = "in_progress"
	// StatusCompleted indicates the task has been finished.
	StatusCompleted TaskStatus = "completed"
)

// IsValid reports whether s is a recognised task status.
func (s TaskStatus) IsValid() bool {
	switch s {
	case StatusPending, StatusInProgress, StatusCompleted:
		return true
	}
	return false
}

// Agenda represents a named plan with an ordered list of tasks.
type Agenda struct {
	ID              int64     `json:"id"`
	IsActive        bool      `json:"is_active"`
	Title           string    `json:"title"`
	Description     string    `json:"description"`
	AcceptanceGuard string    `json:"acceptance_guard,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	Tasks           []Task    `json:"tasks,omitempty"`
}

// Task is a single step within an Agenda.
type Task struct {
	ID         int64      `json:"id"`
	AgendaID   int64      `json:"agenda_id"`
	TaskOrder  int        `json:"task_order"`
	IsOptional bool       `json:"is_optional"`
	Details    string     `json:"details"`
	Status     TaskStatus `json:"status"`
}

// TaskInput is the DTO used to create a new task.
type TaskInput struct {
	Details    string
	IsOptional bool
}

// Engine wraps the agenda database and exposes CRUD operations.
type Engine struct {
	db *sql.DB
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
// Returns the agenda_id.
func (e *Engine) CreateAgenda(title, description, acceptanceGuard string, tasks []TaskInput) (int64, error) {
	tx, err := e.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("create_agenda: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	res, err := tx.Exec(
		`INSERT INTO agendas (title, description, acceptance_guard) VALUES (?, ?, ?)`,
		title, description, acceptanceGuard,
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
			`INSERT INTO tasks (agenda_id, task_order, is_optional, details, status)
			 VALUES (?, ?, ?, ?, ?)`,
			agendaID, i, boolToInt(t.IsOptional), t.Details, string(StatusPending),
		); err != nil {
			return 0, fmt.Errorf("create_agenda: insert task %d: %w", i, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("create_agenda: commit: %w", err)
	}
	return agendaID, nil
}

// ListAgendas returns agendas. If activeOnly is true, only active ones are returned.
func (e *Engine) ListAgendas(activeOnly bool) ([]Agenda, error) {
	q := `SELECT id, is_active, title, description, created_at FROM agendas`
	if activeOnly {
		q += ` WHERE is_active=1`
	}
	q += ` ORDER BY id DESC`

	rows, err := e.db.Query(q)
	if err != nil {
		return nil, fmt.Errorf("list_agendas: query: %w", err)
	}
	defer rows.Close()

	var agendas []Agenda
	for rows.Next() {
		var a Agenda
		var isActive int
		if err := rows.Scan(&a.ID, &isActive, &a.Title, &a.Description, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("list_agendas: scan: %w", err)
		}
		a.IsActive = isActive == 1
		agendas = append(agendas, a)
	}
	return agendas, rows.Err()
}

// GetAgenda returns the full agenda with all tasks ordered by task_order.
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

// SearchAgendas performs FTS5 search on agendas title and description.
func (e *Engine) SearchAgendas(query string, limit int) ([]Agenda, error) {
	if limit <= 0 {
		limit = 10
	}

	safe := fts5Escape(query)
	rows, err := e.db.Query(
		`SELECT a.id, a.is_active, a.title, a.description, a.created_at
		 FROM agendas a
		 JOIN agendas_fts f ON f.rowid = a.id
		 WHERE agendas_fts MATCH ?
		 ORDER BY rank
		 LIMIT ?`,
		safe, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agendas []Agenda
	for rows.Next() {
		var a Agenda
		var isActive int
		if err := rows.Scan(&a.ID, &isActive, &a.Title, &a.Description, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("search_agendas: scan: %w", err)
		}
		a.IsActive = isActive == 1
		agendas = append(agendas, a)
	}
	return agendas, rows.Err()
}

// UpdateTaskByOrder sets the status for a task identified by agenda ID and
// 0-based task order, then runs auto-deactivation.
//
// status must be one of StatusPending, StatusInProgress, or StatusCompleted.
func (e *Engine) UpdateTaskByOrder(agendaID int64, taskOrder int, status TaskStatus) error {
	if !status.IsValid() {
		return fmt.Errorf("update_task: invalid status %q (want pending|in_progress|completed)", status)
	}
	var taskID int64
	err := e.db.QueryRow(
		`SELECT id FROM tasks WHERE agenda_id=? AND task_order=?`,
		agendaID, taskOrder,
	).Scan(&taskID)
	if err != nil {
		return fmt.Errorf("update_task: no task at order %d in agenda %d: %w", taskOrder, agendaID, err)
	}
	return e.UpdateTask(taskID, status)
}

// UpdateTask sets the status for a task and runs auto-deactivation.
//
// status must be one of StatusPending, StatusInProgress, or StatusCompleted.
func (e *Engine) UpdateTask(taskID int64, status TaskStatus) error {
	if !status.IsValid() {
		return fmt.Errorf("update_task: invalid status %q (want pending|in_progress|completed)", status)
	}

	tx, err := e.db.Begin()
	if err != nil {
		return fmt.Errorf("update_task: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Find agenda_id for this task.
	var agendaID int64
	if err := tx.QueryRow(`SELECT agenda_id FROM tasks WHERE id=?`, taskID).Scan(&agendaID); err != nil {
		return fmt.Errorf("update_task: find task: %w", err)
	}

	// Persist new status; keep legacy is_completed in sync for external readers.
	isCompleted := boolToInt(status == StatusCompleted)
	if _, err := tx.Exec(
		`UPDATE tasks SET status=?, is_completed=? WHERE id=?`,
		string(status), isCompleted, taskID,
	); err != nil {
		return fmt.Errorf("update_task: update: %w", err)
	}

	// Auto-deactivation: deactivate the agenda when every required task is
	// completed. Tasks that are in_progress or pending keep the agenda active.
	var incomplete int
	if err := tx.QueryRow(
		`SELECT COUNT(*) FROM tasks WHERE agenda_id=? AND is_optional=0 AND status != 'completed'`,
		agendaID,
	).Scan(&incomplete); err != nil {
		return fmt.Errorf("update_task: check completion: %w", err)
	}
	if incomplete == 0 {
		if _, err := tx.Exec(`UPDATE agendas SET is_active=0 WHERE id=?`, agendaID); err != nil {
			return fmt.Errorf("update_task: deactivate agenda: %w", err)
		}
	}

	return tx.Commit()
}

// UpdateAgenda updates agenda metadata and optionally appends new tasks.
// Pass empty strings for fields that should not change.
// Pass nil for newTasks to skip task appending.
func (e *Engine) UpdateAgenda(id int64, title, description, acceptanceGuard string, isActive *bool, newTasks []TaskInput) error {
	tx, err := e.db.Begin()
	if err != nil {
		return fmt.Errorf("update_agenda: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Build dynamic SET clause.
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
	if isActive != nil {
		setClauses = append(setClauses, "is_active=?")
		args = append(args, boolToInt(*isActive))
	}

	if len(setClauses) > 0 {
		args = append(args, id)
		q := "UPDATE agendas SET " + strings.Join(setClauses, ", ") + " WHERE id=?"
		if _, err := tx.Exec(q, args...); err != nil {
			return fmt.Errorf("update_agenda: update metadata: %w", err)
		}
	}

	// Append new tasks if provided.
	if len(newTasks) > 0 {
		var maxOrder int
		if err := tx.QueryRow(
			`SELECT COALESCE(MAX(task_order), -1) + 1 FROM tasks WHERE agenda_id=?`, id,
		).Scan(&maxOrder); err != nil {
			return fmt.Errorf("update_agenda: get max order: %w", err)
		}
		for i, t := range newTasks {
			if _, err := tx.Exec(
				`INSERT INTO tasks (agenda_id, task_order, is_optional, details, status)
				 VALUES (?, ?, ?, ?, ?)`,
				id, maxOrder+i, boolToInt(t.IsOptional), t.Details, string(StatusPending),
			); err != nil {
				return fmt.Errorf("update_agenda: insert new task %d: %w", i, err)
			}
		}
	}

	return tx.Commit()
}

// AddTask appends a single task to an existing agenda and returns the new task ID.
// The task is inserted with StatusPending and a task_order one higher than the
// current maximum (or 0 if the agenda has no tasks yet).
func (e *Engine) AddTask(agendaID int64, task TaskInput) (int64, error) {
	// Verify the agenda exists.
	var count int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM agendas WHERE id=?`, agendaID).Scan(&count); err != nil {
		return 0, fmt.Errorf("add_task: check agenda: %w", err)
	}
	if count == 0 {
		return 0, fmt.Errorf("add_task: plan %d not found", agendaID)
	}

	var nextOrder int
	if err := e.db.QueryRow(
		`SELECT COALESCE(MAX(task_order), -1) + 1 FROM tasks WHERE agenda_id=?`, agendaID,
	).Scan(&nextOrder); err != nil {
		return 0, fmt.Errorf("add_task: get max order: %w", err)
	}

	res, err := e.db.Exec(
		`INSERT INTO tasks (agenda_id, task_order, is_optional, details, status)
		 VALUES (?, ?, ?, ?, ?)`,
		agendaID, nextOrder, boolToInt(task.IsOptional), task.Details, string(StatusPending),
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

// DeleteAgenda deletes an inactive agenda (and cascades to its tasks).
// Returns an error if the agenda is still active.
func (e *Engine) DeleteAgenda(id int64) error {
	var isActive int
	if err := e.db.QueryRow(`SELECT is_active FROM agendas WHERE id=?`, id).Scan(&isActive); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("delete_agenda: agenda %d not found", id)
		}
		return fmt.Errorf("delete_agenda: check active: %w", err)
	}
	if isActive == 1 {
		return fmt.Errorf("delete_agenda: agenda %d is still active; deactivate it first", id)
	}

	if _, err := e.db.Exec(`DELETE FROM agendas WHERE id=?`, id); err != nil {
		return fmt.Errorf("delete_agenda: delete: %w", err)
	}
	return nil
}

// --- internal helpers ---

func (e *Engine) getAgendaRow(id int64) (*Agenda, error) {
	var a Agenda
	var isActive int
	err := e.db.QueryRow(
		`SELECT id, is_active, title, description, acceptance_guard, created_at FROM agendas WHERE id=?`, id,
	).Scan(&a.ID, &isActive, &a.Title, &a.Description, &a.AcceptanceGuard, &a.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get_agenda: not found: %w", err)
	}
	a.IsActive = isActive == 1
	return &a, nil
}

func (e *Engine) getTasksForAgenda(agendaID int64) ([]Task, error) {
	rows, err := e.db.Query(
		`SELECT id, agenda_id, task_order, is_optional, details, status
		 FROM tasks WHERE agenda_id=? ORDER BY task_order`,
		agendaID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		var isOptional int
		var statusStr string
		if err := rows.Scan(
			&t.ID, &t.AgendaID, &t.TaskOrder, &isOptional, &t.Details, &statusStr,
		); err != nil {
			return nil, err
		}
		t.IsOptional = isOptional == 1
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
