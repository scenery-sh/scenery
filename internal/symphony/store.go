package symphony

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"scenery.sh/internal/sqlitedb"
)

type Store struct {
	db *sql.DB
	mu *sync.Mutex
}

type State struct {
	Statuses []Status `json:"statuses"`
	Tasks    []Task   `json:"tasks"`
	Workflow Workflow `json:"workflow"`
}

type Status struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	SortOrder int    `json:"sort_order"`
	Hidden    bool   `json:"hidden"`
	Color     string `json:"color"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type Task struct {
	ID          string   `json:"id"`
	AppID       string   `json:"app_id"`
	Identifier  string   `json:"identifier"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	StatusKey   string   `json:"status_key"`
	SortOrder   int      `json:"sort_order"`
	Priority    string   `json:"priority"`
	Assignee    string   `json:"assignee"`
	Estimate    string   `json:"estimate"`
	BranchName  string   `json:"branch_name"`
	URL         string   `json:"url"`
	Source      string   `json:"source"`
	Labels      []string `json:"labels"`
	LatestRun   *Run     `json:"latest_run,omitempty"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

type TaskInput struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	StatusKey   string   `json:"status_key"`
	Priority    string   `json:"priority"`
	Assignee    string   `json:"assignee"`
	Estimate    string   `json:"estimate"`
	BranchName  string   `json:"branch_name"`
	URL         string   `json:"url"`
	Source      string   `json:"source"`
	Labels      []string `json:"labels"`
}

type StatusUpdate struct {
	Key       string `json:"key"`
	SortOrder int    `json:"sort_order"`
	Hidden    bool   `json:"hidden"`
}

type Run struct {
	ID             string `json:"id"`
	AppID          string `json:"app_id"`
	TaskID         string `json:"task_id"`
	Attempt        int    `json:"attempt"`
	Status         string `json:"status"`
	WorkspacePath  string `json:"workspace_path"`
	ThreadID       string `json:"thread_id"`
	TurnID         string `json:"turn_id"`
	ProcessID      int    `json:"process_id"`
	OwnerSessionID string `json:"owner_session_id"`
	Summary        string `json:"summary"`
	Error          string `json:"error"`
	StartedAt      string `json:"started_at,omitempty"`
	EndedAt        string `json:"ended_at,omitempty"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

type Workflow struct {
	AppID            string `json:"app_id"`
	WorkflowMarkdown string `json:"workflow_markdown"`
	Mode             string `json:"mode"`
	MaxConcurrency   int    `json:"max_concurrency"`
	UpdatedAt        string `json:"updated_at"`
}

type WorkflowInput struct {
	WorkflowMarkdown string `json:"workflow_markdown"`
	Mode             string `json:"mode"`
	MaxConcurrency   int    `json:"max_concurrency"`
}

var defaultStatuses = []Status{
	{Key: "backlog", Name: "Backlog", Kind: "active", SortOrder: 1000, Color: "neutral"},
	{Key: "todo", Name: "Todo", Kind: "active", SortOrder: 2000, Color: "info"},
	{Key: "in_progress", Name: "In Progress", Kind: "active", SortOrder: 3000, Color: "warning"},
	{Key: "human_review", Name: "Human Review", Kind: "active", SortOrder: 4000, Color: "success"},
	{Key: "rework", Name: "Rework", Kind: "active", SortOrder: 5000, Hidden: true, Color: "warning"},
	{Key: "merging", Name: "Merging", Kind: "active", SortOrder: 6000, Hidden: true, Color: "info"},
	{Key: "done", Name: "Done", Kind: "terminal", SortOrder: 7000, Hidden: true, Color: "success"},
	{Key: "canceled", Name: "Canceled", Kind: "terminal", SortOrder: 8000, Hidden: true, Color: "neutral"},
	{Key: "duplicate", Name: "Duplicate", Kind: "terminal", SortOrder: 9000, Hidden: true, Color: "neutral"},
}

var storeLocks sync.Map

func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sqlitedb.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	lockAny, _ := storeLocks.LoadOrStore(filepath.Clean(path), &sync.Mutex{})
	store := &Store{db: db, mu: lockAny.(*sync.Mutex)}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) State(ctx context.Context, appID string) (State, error) {
	appID, err := cleanAppID(appID)
	if err != nil {
		return State{}, err
	}
	if err := s.ensureApp(ctx, appID); err != nil {
		return State{}, err
	}
	statuses, err := s.listStatuses(ctx, appID)
	if err != nil {
		return State{}, err
	}
	tasks, err := s.listTasks(ctx, appID)
	if err != nil {
		return State{}, err
	}
	workflow, err := s.Workflow(ctx, appID)
	if err != nil {
		return State{}, err
	}
	return State{Statuses: statuses, Tasks: tasks, Workflow: workflow}, nil
}

func (s *Store) CreateTask(ctx context.Context, appID string, input TaskInput) (Task, error) {
	unlock := s.lock()
	defer unlock()

	appID, err := cleanAppID(appID)
	if err != nil {
		return Task{}, err
	}
	input = cleanTaskInput(input)
	if input.Title == "" {
		return Task{}, errors.New("task title is required")
	}
	if input.StatusKey == "" {
		input.StatusKey = "backlog"
	}
	now := nowText()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	if err := ensureAppTx(ctx, tx, appID, now); err != nil {
		return Task{}, err
	}
	if err := statusExistsTx(ctx, tx, appID, input.StatusKey); err != nil {
		return Task{}, err
	}
	identifier, err := nextIdentifierTx(ctx, tx, appID, now)
	if err != nil {
		return Task{}, err
	}
	id, err := randomID("task")
	if err != nil {
		return Task{}, err
	}
	sortOrder, err := nextSortOrderTx(ctx, tx, appID, input.StatusKey)
	if err != nil {
		return Task{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO symphony_tasks (
		id, app_id, identifier, title, description, status_key, sort_order, priority, assignee,
		estimate, branch_name, url, source, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, appID, identifier, input.Title, input.Description, input.StatusKey, sortOrder, input.Priority, input.Assignee,
		input.Estimate, input.BranchName, input.URL, firstNonEmpty(input.Source, "manual"), now, now,
	); err != nil {
		return Task{}, err
	}
	if err := replaceLabelsTx(ctx, tx, appID, id, input.Labels); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return s.Task(ctx, appID, id)
}

func (s *Store) UpdateTask(ctx context.Context, appID, id string, input TaskInput) (Task, error) {
	unlock := s.lock()
	defer unlock()

	appID, err := cleanAppID(appID)
	if err != nil {
		return Task{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return Task{}, errors.New("task id is required")
	}
	input = cleanTaskInput(input)
	if input.Title == "" {
		return Task{}, errors.New("task title is required")
	}
	now := nowText()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	if err := ensureAppTx(ctx, tx, appID, now); err != nil {
		return Task{}, err
	}
	current, err := taskTx(ctx, tx, appID, id)
	if err != nil {
		return Task{}, err
	}
	if input.StatusKey == "" {
		input.StatusKey = current.StatusKey
	}
	if err := statusExistsTx(ctx, tx, appID, input.StatusKey); err != nil {
		return Task{}, err
	}
	sortOrder := current.SortOrder
	if input.StatusKey != current.StatusKey {
		sortOrder, err = nextSortOrderTx(ctx, tx, appID, input.StatusKey)
		if err != nil {
			return Task{}, err
		}
	}
	res, err := tx.ExecContext(ctx, `UPDATE symphony_tasks SET
		title = ?, description = ?, status_key = ?, sort_order = ?, priority = ?, assignee = ?,
		estimate = ?, branch_name = ?, url = ?, source = ?, updated_at = ?
		WHERE app_id = ? AND id = ? AND deleted_at IS NULL`,
		input.Title, input.Description, input.StatusKey, sortOrder, input.Priority, input.Assignee,
		input.Estimate, input.BranchName, input.URL, firstNonEmpty(input.Source, current.Source, "manual"), now, appID, id,
	)
	if err != nil {
		return Task{}, err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return Task{}, sql.ErrNoRows
	}
	if err := replaceLabelsTx(ctx, tx, appID, id, input.Labels); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return s.Task(ctx, appID, id)
}

func (s *Store) MoveTask(ctx context.Context, appID, id, statusKey string, index int) error {
	unlock := s.lock()
	defer unlock()

	appID, err := cleanAppID(appID)
	if err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	statusKey = strings.TrimSpace(statusKey)
	if id == "" || statusKey == "" {
		return errors.New("task id and status key are required")
	}
	now := nowText()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := ensureAppTx(ctx, tx, appID, now); err != nil {
		return err
	}
	current, err := taskTx(ctx, tx, appID, id)
	if err != nil {
		return err
	}
	if err := statusExistsTx(ctx, tx, appID, statusKey); err != nil {
		return err
	}
	targetIDs, err := taskIDsForStatusTx(ctx, tx, appID, statusKey, id)
	if err != nil {
		return err
	}
	if index < 0 {
		index = 0
	}
	if index > len(targetIDs) {
		index = len(targetIDs)
	}
	targetIDs = append(targetIDs, "")
	copy(targetIDs[index+1:], targetIDs[index:])
	targetIDs[index] = id
	if err := renumberStatusTx(ctx, tx, appID, statusKey, targetIDs, now); err != nil {
		return err
	}
	if current.StatusKey != statusKey {
		prevIDs, err := taskIDsForStatusTx(ctx, tx, appID, current.StatusKey, id)
		if err != nil {
			return err
		}
		if err := renumberStatusTx(ctx, tx, appID, current.StatusKey, prevIDs, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) DeleteTask(ctx context.Context, appID, id string) error {
	unlock := s.lock()
	defer unlock()

	appID, err := cleanAppID(appID)
	if err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("task id is required")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE symphony_tasks SET deleted_at = ?, updated_at = ? WHERE app_id = ? AND id = ? AND deleted_at IS NULL`, nowText(), nowText(), appID, id)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) Task(ctx context.Context, appID, id string) (Task, error) {
	appID, err := cleanAppID(appID)
	if err != nil {
		return Task{}, err
	}
	rows, err := s.db.QueryContext(ctx, taskSelectSQL()+` AND t.id = ?`, appID, id)
	if err != nil {
		return Task{}, err
	}
	defer rows.Close()
	tasks, err := scanTasks(rows)
	if err != nil {
		return Task{}, err
	}
	if len(tasks) == 0 {
		return Task{}, sql.ErrNoRows
	}
	if err := s.attachLabels(ctx, appID, tasks); err != nil {
		return Task{}, err
	}
	return tasks[0], nil
}

func (s *Store) UpdateStatuses(ctx context.Context, appID string, updates []StatusUpdate) (State, error) {
	if err := s.updateStatuses(ctx, appID, updates); err != nil {
		return State{}, err
	}
	return s.State(ctx, appID)
}

func (s *Store) updateStatuses(ctx context.Context, appID string, updates []StatusUpdate) error {
	unlock := s.lock()
	defer unlock()

	appID, err := cleanAppID(appID)
	if err != nil {
		return err
	}
	now := nowText()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := ensureAppTx(ctx, tx, appID, now); err != nil {
		return err
	}
	seen := map[string]bool{}
	for i, update := range updates {
		key := strings.TrimSpace(update.Key)
		if key == "" || seen[key] {
			return fmt.Errorf("invalid status update %q", key)
		}
		seen[key] = true
		sortOrder := update.SortOrder
		if sortOrder <= 0 {
			sortOrder = (i + 1) * 1000
		}
		res, err := tx.ExecContext(ctx, `UPDATE symphony_statuses SET sort_order = ?, hidden = ?, updated_at = ? WHERE app_id = ? AND status_key = ?`, sortOrder, boolInt(update.Hidden), now, appID, key)
		if err != nil {
			return err
		}
		if rows, _ := res.RowsAffected(); rows == 0 {
			return fmt.Errorf("unknown status %q", key)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *Store) Workflow(ctx context.Context, appID string) (Workflow, error) {
	appID, err := cleanAppID(appID)
	if err != nil {
		return Workflow{}, err
	}
	var workflow Workflow
	err = s.db.QueryRowContext(ctx, `SELECT app_id, workflow_markdown, mode, max_concurrency, updated_at FROM symphony_workflows WHERE app_id = ?`, appID).Scan(
		&workflow.AppID, &workflow.WorkflowMarkdown, &workflow.Mode, &workflow.MaxConcurrency, &workflow.UpdatedAt,
	)
	if err == nil {
		return workflow, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Workflow{}, err
	}
	return Workflow{AppID: appID, Mode: "manual", MaxConcurrency: 1}, nil
}

func (s *Store) UpdateWorkflow(ctx context.Context, appID string, input WorkflowInput) (Workflow, error) {
	unlock := s.lock()
	defer unlock()

	appID, err := cleanAppID(appID)
	if err != nil {
		return Workflow{}, err
	}
	mode := strings.TrimSpace(input.Mode)
	if mode == "" {
		mode = "manual"
	}
	if mode != "manual" && mode != "disabled" {
		return Workflow{}, fmt.Errorf("unsupported workflow mode %q", mode)
	}
	maxConcurrency := input.MaxConcurrency
	if maxConcurrency <= 0 {
		maxConcurrency = 1
	}
	now := nowText()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO symphony_workflows (app_id, workflow_markdown, mode, max_concurrency, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(app_id) DO UPDATE SET workflow_markdown = excluded.workflow_markdown, mode = excluded.mode, max_concurrency = excluded.max_concurrency, updated_at = excluded.updated_at`,
		appID, input.WorkflowMarkdown, mode, maxConcurrency, now,
	); err != nil {
		return Workflow{}, err
	}
	return s.Workflow(ctx, appID)
}

func (s *Store) migrate(ctx context.Context) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS symphony_statuses (
			app_id text not null,
			status_key text not null,
			name text not null,
			kind text not null,
			sort_order integer not null,
			hidden integer not null default 0,
			color text not null,
			created_at text not null,
			updated_at text not null,
			primary key(app_id, status_key)
		)`,
		`CREATE TABLE IF NOT EXISTS symphony_tasks (
			id text primary key,
			app_id text not null,
			identifier text not null,
			title text not null,
			description text not null default '',
			status_key text not null,
			sort_order integer not null,
			priority text not null default '',
			assignee text not null default '',
			estimate text not null default '',
			branch_name text not null default '',
			url text not null default '',
			source text not null default 'manual',
			created_at text not null,
			updated_at text not null,
			deleted_at text,
			unique(app_id, identifier),
			foreign key(app_id, status_key) references symphony_statuses(app_id, status_key)
		)`,
		`CREATE INDEX IF NOT EXISTS symphony_tasks_board_idx ON symphony_tasks(app_id, status_key, sort_order, updated_at)`,
		`CREATE TABLE IF NOT EXISTS symphony_app_counters (
			app_id text not null,
			counter_key text not null,
			next_value integer not null,
			updated_at text not null,
			primary key(app_id, counter_key)
		)`,
		`CREATE TABLE IF NOT EXISTS symphony_task_labels (
			app_id text not null,
			task_id text not null,
			label text not null,
			primary key(app_id, task_id, label),
			foreign key(task_id) references symphony_tasks(id)
		)`,
		`CREATE TABLE IF NOT EXISTS symphony_runs (
			id text primary key,
			app_id text not null,
			task_id text not null,
			attempt integer not null,
			status text not null,
			workspace_path text not null default '',
			thread_id text not null default '',
			turn_id text not null default '',
			process_id integer not null default 0,
			owner_session_id text not null default '',
			owner_started_at text,
			lease_expires_at text,
			summary text not null default '',
			error text not null default '',
			started_at text,
			ended_at text,
			created_at text not null,
			updated_at text not null,
			foreign key(task_id) references symphony_tasks(id)
		)`,
		`CREATE INDEX IF NOT EXISTS symphony_runs_task_idx ON symphony_runs(app_id, task_id, created_at)`,
		`CREATE TABLE IF NOT EXISTS symphony_run_events (
			app_id text not null,
			run_id text not null,
			seq integer not null,
			type text not null,
			payload_json text not null,
			created_at text not null,
			primary key(app_id, run_id, seq),
			foreign key(run_id) references symphony_runs(id)
		)`,
		`CREATE TABLE IF NOT EXISTS symphony_workflows (
			app_id text primary key,
			workflow_markdown text not null,
			mode text not null default 'manual',
			max_concurrency integer not null default 1,
			updated_at text not null
		)`,
		`INSERT INTO scenery_sqlite_metadata (key, value) VALUES ('symphony_schema_version', '1')
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
	} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ensureApp(ctx context.Context, appID string) error {
	unlock := s.lock()
	defer unlock()

	now := nowText()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := ensureAppTx(ctx, tx, appID, now); err != nil {
		return err
	}
	return tx.Commit()
}

func ensureAppTx(ctx context.Context, tx *sql.Tx, appID, now string) error {
	for _, status := range defaultStatuses {
		if _, err := tx.ExecContext(ctx, `INSERT INTO symphony_statuses (
			app_id, status_key, name, kind, sort_order, hidden, color, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(app_id, status_key) DO NOTHING`,
			appID, status.Key, status.Name, status.Kind, status.SortOrder, boolInt(status.Hidden), status.Color, now, now,
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) listStatuses(ctx context.Context, appID string) ([]Status, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status_key, name, kind, sort_order, hidden, color, created_at, updated_at
		FROM symphony_statuses WHERE app_id = ? ORDER BY sort_order, status_key`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Status
	for rows.Next() {
		var status Status
		var hidden int
		if err := rows.Scan(&status.Key, &status.Name, &status.Kind, &status.SortOrder, &hidden, &status.Color, &status.CreatedAt, &status.UpdatedAt); err != nil {
			return nil, err
		}
		status.Hidden = hidden != 0
		out = append(out, status)
	}
	return out, rows.Err()
}

func (s *Store) listTasks(ctx context.Context, appID string) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx, taskSelectSQL()+` ORDER BY st.sort_order, t.sort_order, t.updated_at DESC`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks, err := scanTasks(rows)
	if err != nil {
		return nil, err
	}
	if err := s.attachLabels(ctx, appID, tasks); err != nil {
		return nil, err
	}
	if err := s.attachLatestRuns(ctx, appID, tasks); err != nil {
		return nil, err
	}
	return tasks, nil
}

func taskSelectSQL() string {
	return `SELECT t.id, t.app_id, t.identifier, t.title, t.description, t.status_key, t.sort_order,
		t.priority, t.assignee, t.estimate, t.branch_name, t.url, t.source, t.created_at, t.updated_at
		FROM symphony_tasks t
		JOIN symphony_statuses st ON st.app_id = t.app_id AND st.status_key = t.status_key
		WHERE t.app_id = ? AND t.deleted_at IS NULL`
}

func scanTasks(rows *sql.Rows) ([]Task, error) {
	var out []Task
	for rows.Next() {
		var task Task
		if err := rows.Scan(
			&task.ID, &task.AppID, &task.Identifier, &task.Title, &task.Description, &task.StatusKey, &task.SortOrder,
			&task.Priority, &task.Assignee, &task.Estimate, &task.BranchName, &task.URL, &task.Source, &task.CreatedAt, &task.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, task)
	}
	return out, rows.Err()
}

func (s *Store) attachLabels(ctx context.Context, appID string, tasks []Task) error {
	if len(tasks) == 0 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT task_id, label FROM symphony_task_labels WHERE app_id = ? ORDER BY label`, appID)
	if err != nil {
		return err
	}
	defer rows.Close()
	labels := map[string][]string{}
	for rows.Next() {
		var taskID, label string
		if err := rows.Scan(&taskID, &label); err != nil {
			return err
		}
		labels[taskID] = append(labels[taskID], label)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range tasks {
		tasks[i].Labels = labels[tasks[i].ID]
		if tasks[i].Labels == nil {
			tasks[i].Labels = []string{}
		}
	}
	return nil
}

func (s *Store) attachLatestRuns(ctx context.Context, appID string, tasks []Task) error {
	if len(tasks) == 0 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.id, r.app_id, r.task_id, r.attempt, r.status, r.workspace_path,
		r.thread_id, r.turn_id, r.process_id, r.owner_session_id, r.summary, r.error,
		coalesce(r.started_at, ''), coalesce(r.ended_at, ''), r.created_at, r.updated_at
		FROM symphony_runs r
		JOIN (
			SELECT task_id, max(created_at) AS created_at
			FROM symphony_runs
			WHERE app_id = ?
			GROUP BY task_id
		) latest ON latest.task_id = r.task_id AND latest.created_at = r.created_at
		WHERE r.app_id = ?`, appID, appID)
	if err != nil {
		return err
	}
	defer rows.Close()
	runs := map[string]Run{}
	for rows.Next() {
		var run Run
		if err := rows.Scan(&run.ID, &run.AppID, &run.TaskID, &run.Attempt, &run.Status, &run.WorkspacePath, &run.ThreadID, &run.TurnID, &run.ProcessID, &run.OwnerSessionID, &run.Summary, &run.Error, &run.StartedAt, &run.EndedAt, &run.CreatedAt, &run.UpdatedAt); err != nil {
			return err
		}
		runs[run.TaskID] = run
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range tasks {
		if run, ok := runs[tasks[i].ID]; ok {
			tasks[i].LatestRun = &run
		}
	}
	return nil
}

func cleanAppID(appID string) (string, error) {
	appID = strings.TrimSpace(appID)
	if appID == "" {
		return "", errors.New("app id is required")
	}
	return appID, nil
}

func cleanTaskInput(input TaskInput) TaskInput {
	input.Title = strings.TrimSpace(input.Title)
	input.StatusKey = strings.TrimSpace(input.StatusKey)
	input.Priority = strings.TrimSpace(input.Priority)
	input.Assignee = strings.TrimSpace(input.Assignee)
	input.Estimate = strings.TrimSpace(input.Estimate)
	input.BranchName = strings.TrimSpace(input.BranchName)
	input.URL = strings.TrimSpace(input.URL)
	input.Source = strings.TrimSpace(input.Source)
	input.Labels = cleanLabels(input.Labels)
	return input
}

func cleanLabels(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		label := strings.TrimSpace(value)
		if label == "" || seen[label] {
			continue
		}
		seen[label] = true
		out = append(out, label)
	}
	sort.Strings(out)
	return out
}

func statusExistsTx(ctx context.Context, tx *sql.Tx, appID, key string) error {
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM symphony_statuses WHERE app_id = ? AND status_key = ?`, appID, key).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("unknown status %q", key)
		}
		return err
	}
	return nil
}

func taskTx(ctx context.Context, tx *sql.Tx, appID, id string) (Task, error) {
	var task Task
	err := tx.QueryRowContext(ctx, `SELECT id, app_id, identifier, title, description, status_key, sort_order,
		priority, assignee, estimate, branch_name, url, source, created_at, updated_at
		FROM symphony_tasks WHERE app_id = ? AND id = ? AND deleted_at IS NULL`, appID, id).Scan(
		&task.ID, &task.AppID, &task.Identifier, &task.Title, &task.Description, &task.StatusKey, &task.SortOrder,
		&task.Priority, &task.Assignee, &task.Estimate, &task.BranchName, &task.URL, &task.Source, &task.CreatedAt, &task.UpdatedAt,
	)
	return task, err
}

func nextIdentifierTx(ctx context.Context, tx *sql.Tx, appID, now string) (string, error) {
	var next int
	err := tx.QueryRowContext(ctx, `INSERT INTO symphony_app_counters (app_id, counter_key, next_value, updated_at)
		VALUES (?, 'task', 2, ?)
		ON CONFLICT(app_id, counter_key) DO UPDATE SET next_value = next_value + 1, updated_at = excluded.updated_at
		RETURNING next_value - 1`, appID, now).Scan(&next)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("SYM-%d", next), nil
}

func nextSortOrderTx(ctx context.Context, tx *sql.Tx, appID, statusKey string) (int, error) {
	var order int
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(sort_order), 0) + 1000 FROM symphony_tasks WHERE app_id = ? AND status_key = ? AND deleted_at IS NULL`, appID, statusKey).Scan(&order); err != nil {
		return 0, err
	}
	return order, nil
}

func replaceLabelsTx(ctx context.Context, tx *sql.Tx, appID, taskID string, labels []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM symphony_task_labels WHERE app_id = ? AND task_id = ?`, appID, taskID); err != nil {
		return err
	}
	for _, label := range cleanLabels(labels) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO symphony_task_labels (app_id, task_id, label) VALUES (?, ?, ?)`, appID, taskID, label); err != nil {
			return err
		}
	}
	return nil
}

func taskIDsForStatusTx(ctx context.Context, tx *sql.Tx, appID, statusKey, excludeID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM symphony_tasks WHERE app_id = ? AND status_key = ? AND deleted_at IS NULL AND id <> ? ORDER BY sort_order, updated_at DESC`, appID, statusKey, excludeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func renumberStatusTx(ctx context.Context, tx *sql.Tx, appID, statusKey string, ids []string, now string) error {
	for i, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE symphony_tasks SET status_key = ?, sort_order = ?, updated_at = ? WHERE app_id = ? AND id = ? AND deleted_at IS NULL`, statusKey, (i+1)*1000, now, appID, id); err != nil {
			return err
		}
	}
	return nil
}

func randomID(prefix string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(b[:]), nil
}

func nowText() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (s *Store) lock() func() {
	if s == nil || s.mu == nil {
		return func() {}
	}
	s.mu.Lock()
	return s.mu.Unlock
}
