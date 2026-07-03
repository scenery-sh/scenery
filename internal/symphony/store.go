package symphony

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
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
	RepoRoot       string `json:"repo_root,omitempty"`
	RepoWorkspace  string `json:"repo_workspace_path,omitempty"`
	WorkspacePath  string `json:"workspace_path"`
	ThreadID       string `json:"thread_id"`
	TurnID         string `json:"turn_id"`
	ProcessID      int    `json:"process_id"`
	OwnerSessionID string `json:"owner_session_id"`
	OwnerStartedAt string `json:"owner_started_at,omitempty"`
	LeaseExpiresAt string `json:"lease_expires_at,omitempty"`
	Summary        string `json:"summary"`
	Error          string `json:"error"`
	DiffStat       string `json:"diff_stat"`
	Diff           string `json:"diff"`
	StartedAt      string `json:"started_at,omitempty"`
	EndedAt        string `json:"ended_at,omitempty"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

type RunEvent struct {
	AppID       string          `json:"app_id"`
	RunID       string          `json:"run_id"`
	Seq         int             `json:"seq"`
	Type        string          `json:"type"`
	PayloadJSON json.RawMessage `json:"payload_json"`
	CreatedAt   string          `json:"created_at"`
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

var activeRunStatuses = map[string]bool{
	"queued":  true,
	"running": true,
}

const DefaultRunLeaseDuration = time.Minute

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
	if mode != "manual" && mode != "auto" && mode != "disabled" {
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

func (s *Store) RunnableTasks(ctx context.Context, appID string, statusKeys []string, limit int) ([]Task, error) {
	return s.RunnableTasksWithMaxAttempts(ctx, appID, statusKeys, limit, 0)
}

func (s *Store) RunnableTasksWithMaxAttempts(ctx context.Context, appID string, statusKeys []string, limit, maxAttempts int) ([]Task, error) {
	appID, err := cleanAppID(appID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureApp(ctx, appID); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 1
	}
	statusKeys = cleanStatusKeys(statusKeys)
	if len(statusKeys) == 0 {
		statusKeys = []string{"todo"}
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(statusKeys)), ",")
	args := []any{appID}
	for _, key := range statusKeys {
		args = append(args, key)
	}
	attemptFilter := ""
	if maxAttempts > 0 {
		attemptFilter = ` AND (
			SELECT count(*) FROM symphony_runs attempts WHERE attempts.app_id = t.app_id AND attempts.task_id = t.id
		) < ?`
		args = append(args, maxAttempts)
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, taskSelectSQL()+` AND t.status_key IN (`+placeholders+`) AND NOT EXISTS (
			SELECT 1 FROM symphony_runs r WHERE r.app_id = t.app_id AND r.task_id = t.id AND r.status IN ('queued', 'running')
		)`+attemptFilter+` ORDER BY st.sort_order, t.sort_order, t.updated_at DESC LIMIT ?`, args...)
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

func (s *Store) ActiveRunCount(ctx context.Context, appID string) (int, error) {
	appID, err := cleanAppID(appID)
	if err != nil {
		return 0, err
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM symphony_runs WHERE app_id = ? AND status IN ('queued', 'running')`, appID).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) StartRun(ctx context.Context, appID, taskID, workspacePath, ownerSessionID string) (Run, error) {
	return s.StartRunWithRepo(ctx, appID, taskID, workspacePath, ownerSessionID, "", "")
}

func (s *Store) StartRunWithRepo(ctx context.Context, appID, taskID, workspacePath, ownerSessionID, repoRoot, repoWorkspace string) (Run, error) {
	unlock := s.lock()
	defer unlock()

	appID, err := cleanAppID(appID)
	if err != nil {
		return Run{}, err
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return Run{}, errors.New("task id is required")
	}
	workspacePath = strings.TrimSpace(workspacePath)
	ownerSessionID = strings.TrimSpace(ownerSessionID)
	repoRoot = strings.TrimSpace(repoRoot)
	repoWorkspace = strings.TrimSpace(repoWorkspace)
	nowTime := time.Now().UTC()
	now := nowTime.Format(time.RFC3339Nano)
	leaseExpiresAt := nowTime.Add(DefaultRunLeaseDuration).Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback()
	if err := ensureAppTx(ctx, tx, appID, now); err != nil {
		return Run{}, err
	}
	if _, err := taskTx(ctx, tx, appID, taskID); err != nil {
		return Run{}, err
	}
	if err := noActiveRunTx(ctx, tx, appID, taskID); err != nil {
		return Run{}, err
	}
	attempt, err := nextRunAttemptTx(ctx, tx, appID, taskID)
	if err != nil {
		return Run{}, err
	}
	id, err := randomID("run")
	if err != nil {
		return Run{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO symphony_runs (
		id, app_id, task_id, attempt, status, repo_root, repo_workspace_path, workspace_path, owner_session_id,
		owner_started_at, lease_expires_at, created_at, updated_at
	) VALUES (?, ?, ?, ?, 'queued', ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, appID, taskID, attempt, repoRoot, repoWorkspace, workspacePath, ownerSessionID, now, leaseExpiresAt, now, now,
	); err != nil {
		return Run{}, err
	}
	if err := appendRunEventTx(ctx, tx, appID, id, "run.queued", map[string]any{"workspace_path": workspacePath, "repo_workspace_path": repoWorkspace, "lease_expires_at": leaseExpiresAt}, now); err != nil {
		return Run{}, err
	}
	if err := tx.Commit(); err != nil {
		return Run{}, err
	}
	return s.Run(ctx, appID, id)
}

func (s *Store) MarkRunRunning(ctx context.Context, appID, runID string, processID int, threadID string) (Run, error) {
	return s.updateRun(ctx, appID, runID, func(ctx context.Context, tx *sql.Tx, now string) error {
		leaseExpiresAt := time.Now().UTC().Add(DefaultRunLeaseDuration).Format(time.RFC3339Nano)
		result, err := tx.ExecContext(ctx, `UPDATE symphony_runs SET status = 'running', process_id = ?, thread_id = ?, lease_expires_at = ?, started_at = coalesce(started_at, ?), updated_at = ? WHERE app_id = ? AND id = ? AND status IN ('queued', 'running')`,
			processID, strings.TrimSpace(threadID), leaseExpiresAt, now, now, appID, runID,
		)
		if err != nil {
			return err
		}
		if rows, _ := result.RowsAffected(); rows == 0 {
			return nil
		}
		return appendRunEventTx(ctx, tx, appID, runID, "run.started", map[string]any{"process_id": processID, "thread_id": strings.TrimSpace(threadID)}, now)
	})
}

func (s *Store) MarkRunTurn(ctx context.Context, appID, runID, turnID string) (Run, error) {
	return s.updateRun(ctx, appID, runID, func(ctx context.Context, tx *sql.Tx, now string) error {
		turnID = strings.TrimSpace(turnID)
		result, err := tx.ExecContext(ctx, `UPDATE symphony_runs SET turn_id = ?, updated_at = ? WHERE app_id = ? AND id = ? AND status IN ('queued', 'running')`, turnID, now, appID, runID)
		if err != nil {
			return err
		}
		if rows, _ := result.RowsAffected(); rows == 0 {
			return nil
		}
		return appendRunEventTx(ctx, tx, appID, runID, "turn.started", map[string]any{"turn_id": turnID}, now)
	})
}

func (s *Store) RenewRunLease(ctx context.Context, appID, runID string, duration time.Duration) (Run, error) {
	if duration == 0 {
		duration = DefaultRunLeaseDuration
	}
	expiresAt := time.Now().UTC().Add(duration).Format(time.RFC3339Nano)
	return s.updateRun(ctx, appID, runID, func(ctx context.Context, tx *sql.Tx, now string) error {
		_, err := tx.ExecContext(ctx, `UPDATE symphony_runs SET lease_expires_at = ?, updated_at = ? WHERE app_id = ? AND id = ? AND status IN ('queued', 'running')`, expiresAt, now, appID, runID)
		return err
	})
}

func (s *Store) MarkExpiredRunsStalled(ctx context.Context, appID string) (int, error) {
	unlock := s.lock()
	defer unlock()

	appID, err := cleanAppID(appID)
	if err != nil {
		return 0, err
	}
	nowTime := time.Now().UTC()
	now := nowTime.Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id, task_id, coalesce(lease_expires_at, '') FROM symphony_runs WHERE app_id = ? AND status IN ('queued', 'running')`, appID)
	if err != nil {
		return 0, err
	}
	type expiredRun struct {
		id             string
		taskID         string
		leaseExpiresAt string
		reason         string
	}
	var expired []expiredRun
	for rows.Next() {
		var item expiredRun
		if err := rows.Scan(&item.id, &item.taskID, &item.leaseExpiresAt); err != nil {
			_ = rows.Close()
			return 0, err
		}
		item.reason = "lease expired"
		if strings.TrimSpace(item.leaseExpiresAt) == "" {
			item.reason = "missing lease"
			expired = append(expired, item)
			continue
		}
		leaseTime, err := time.Parse(time.RFC3339Nano, item.leaseExpiresAt)
		if err != nil || !leaseTime.After(nowTime) {
			if err != nil {
				item.reason = "invalid lease"
			}
			expired = append(expired, item)
		}
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	marked := 0
	for _, item := range expired {
		result, err := tx.ExecContext(ctx, `UPDATE symphony_runs SET status = 'stalled', error = ?, ended_at = coalesce(ended_at, ?), updated_at = ? WHERE app_id = ? AND id = ? AND status IN ('queued', 'running')`,
			item.reason, now, now, appID, item.id,
		)
		if err != nil {
			return 0, err
		}
		rowsAffected, _ := result.RowsAffected()
		if rowsAffected == 0 {
			continue
		}
		marked++
		if err := appendRunEventTx(ctx, tx, appID, item.id, "run.stalled", map[string]any{"reason": item.reason, "lease_expires_at": item.leaseExpiresAt}, now); err != nil {
			return 0, err
		}
		task, err := taskTx(ctx, tx, appID, item.taskID)
		if err != nil {
			return 0, err
		}
		if task.StatusKey == "todo" || task.StatusKey == "in_progress" {
			sortOrder := task.SortOrder
			if task.StatusKey != "todo" {
				sortOrder, err = nextSortOrderTx(ctx, tx, appID, "todo")
				if err != nil {
					return 0, err
				}
			}
			if _, err := tx.ExecContext(ctx, `UPDATE symphony_tasks SET status_key = 'todo', sort_order = ?, updated_at = ? WHERE app_id = ? AND id = ? AND deleted_at IS NULL`, sortOrder, now, appID, item.taskID); err != nil {
				return 0, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return marked, nil
}

func (s *Store) CompleteRun(ctx context.Context, appID, runID, status, summary, runError string) (Run, error) {
	status = strings.TrimSpace(status)
	if status == "" {
		status = "succeeded"
	}
	if activeRunStatuses[status] {
		return Run{}, fmt.Errorf("completion status %q is still active", status)
	}
	return s.updateRun(ctx, appID, runID, func(ctx context.Context, tx *sql.Tx, now string) error {
		result, err := tx.ExecContext(ctx, `UPDATE symphony_runs SET status = ?, summary = ?, error = ?, ended_at = coalesce(ended_at, ?), updated_at = ? WHERE app_id = ? AND id = ? AND status IN ('queued', 'running')`,
			status, strings.TrimSpace(summary), strings.TrimSpace(runError), now, now, appID, runID,
		)
		if err != nil {
			return err
		}
		if rows, _ := result.RowsAffected(); rows == 0 {
			return nil
		}
		return appendRunEventTx(ctx, tx, appID, runID, "run."+status, map[string]any{"summary": strings.TrimSpace(summary), "error": strings.TrimSpace(runError)}, now)
	})
}

func (s *Store) RecordRunArtifacts(ctx context.Context, appID, runID, diffStat, diff string) (Run, error) {
	return s.updateRun(ctx, appID, runID, func(ctx context.Context, tx *sql.Tx, now string) error {
		_, err := tx.ExecContext(ctx, `UPDATE symphony_runs SET diff_stat = ?, diff = ?, updated_at = ? WHERE app_id = ? AND id = ?`,
			strings.TrimSpace(diffStat), strings.TrimSpace(diff), now, appID, runID,
		)
		if err != nil {
			return err
		}
		return appendRunEventTx(ctx, tx, appID, runID, "run.artifacts", map[string]any{"has_diff": strings.TrimSpace(diff) != "", "has_diff_stat": strings.TrimSpace(diffStat) != ""}, now)
	})
}

func (s *Store) RecordRunEvent(ctx context.Context, appID, runID, eventType string, payload any) error {
	unlock := s.lock()
	defer unlock()

	appID, err := cleanAppID(appID)
	if err != nil {
		return err
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return errors.New("run id is required")
	}
	now := nowText()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := runExistsTx(ctx, tx, appID, runID); err != nil {
		return err
	}
	if err := appendRunEventTx(ctx, tx, appID, runID, eventType, payload, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Run(ctx context.Context, appID, runID string) (Run, error) {
	appID, err := cleanAppID(appID)
	if err != nil {
		return Run{}, err
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return Run{}, errors.New("run id is required")
	}
	rows, err := s.db.QueryContext(ctx, runSelectSQL()+` WHERE app_id = ? AND id = ?`, appID, runID)
	if err != nil {
		return Run{}, err
	}
	defer rows.Close()
	runs, err := scanRuns(rows)
	if err != nil {
		return Run{}, err
	}
	if len(runs) == 0 {
		return Run{}, sql.ErrNoRows
	}
	return runs[0], nil
}

func (s *Store) RunEvents(ctx context.Context, appID, runID string) ([]RunEvent, error) {
	appID, err := cleanAppID(appID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT app_id, run_id, seq, type, payload_json, created_at FROM symphony_run_events WHERE app_id = ? AND run_id = ? ORDER BY seq`, appID, strings.TrimSpace(runID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunEvent
	for rows.Next() {
		var event RunEvent
		var payload string
		if err := rows.Scan(&event.AppID, &event.RunID, &event.Seq, &event.Type, &payload, &event.CreatedAt); err != nil {
			return nil, err
		}
		event.PayloadJSON = json.RawMessage(payload)
		out = append(out, event)
	}
	return out, rows.Err()
}

func (s *Store) TerminalWorkspaces(ctx context.Context, appID string) ([]Run, error) {
	appID, err := cleanAppID(appID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, runSelectSQL()+` JOIN symphony_tasks t ON t.app_id = symphony_runs.app_id AND t.id = symphony_runs.task_id
		JOIN symphony_statuses st ON st.app_id = t.app_id AND st.status_key = t.status_key
		WHERE symphony_runs.app_id = ? AND st.kind = 'terminal' AND symphony_runs.repo_workspace_path <> ''`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRuns(rows)
}

func (s *Store) updateRun(ctx context.Context, appID, runID string, fn func(context.Context, *sql.Tx, string) error) (Run, error) {
	unlock := s.lock()
	defer unlock()

	appID, err := cleanAppID(appID)
	if err != nil {
		return Run{}, err
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return Run{}, errors.New("run id is required")
	}
	now := nowText()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback()
	if err := runExistsTx(ctx, tx, appID, runID); err != nil {
		return Run{}, err
	}
	if err := fn(ctx, tx, now); err != nil {
		return Run{}, err
	}
	if err := tx.Commit(); err != nil {
		return Run{}, err
	}
	return s.Run(ctx, appID, runID)
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
			repo_root text not null default '',
			repo_workspace_path text not null default '',
			workspace_path text not null default '',
			thread_id text not null default '',
			turn_id text not null default '',
			process_id integer not null default 0,
			owner_session_id text not null default '',
			owner_started_at text,
			lease_expires_at text,
			summary text not null default '',
			error text not null default '',
			diff_stat text not null default '',
			diff text not null default '',
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
	for _, stmt := range []string{
		`ALTER TABLE symphony_runs ADD COLUMN repo_root text not null default ''`,
		`ALTER TABLE symphony_runs ADD COLUMN repo_workspace_path text not null default ''`,
		`ALTER TABLE symphony_runs ADD COLUMN owner_started_at text`,
		`ALTER TABLE symphony_runs ADD COLUMN lease_expires_at text`,
		`ALTER TABLE symphony_runs ADD COLUMN diff_stat text not null default ''`,
		`ALTER TABLE symphony_runs ADD COLUMN diff text not null default ''`,
	} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
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
	rows, err := s.db.QueryContext(ctx, `SELECT r.id, r.app_id, r.task_id, r.attempt, r.status,
		r.repo_root, r.repo_workspace_path, r.workspace_path, r.thread_id, r.turn_id, r.process_id,
		r.owner_session_id, coalesce(r.owner_started_at, ''), coalesce(r.lease_expires_at, ''), r.summary, r.error,
		r.diff_stat, r.diff, coalesce(r.started_at, ''), coalesce(r.ended_at, ''), r.created_at, r.updated_at
		FROM symphony_runs r
		JOIN (
			SELECT task_id, max(attempt) AS attempt
			FROM symphony_runs
			WHERE app_id = ?
			GROUP BY task_id
		) latest ON latest.task_id = r.task_id AND latest.attempt = r.attempt
		WHERE r.app_id = ?`, appID, appID)
	if err != nil {
		return err
	}
	defer rows.Close()
	runs := map[string]Run{}
	for rows.Next() {
		var run Run
		if err := rows.Scan(&run.ID, &run.AppID, &run.TaskID, &run.Attempt, &run.Status, &run.RepoRoot, &run.RepoWorkspace, &run.WorkspacePath, &run.ThreadID, &run.TurnID, &run.ProcessID, &run.OwnerSessionID, &run.OwnerStartedAt, &run.LeaseExpiresAt, &run.Summary, &run.Error, &run.DiffStat, &run.Diff, &run.StartedAt, &run.EndedAt, &run.CreatedAt, &run.UpdatedAt); err != nil {
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

func runSelectSQL() string {
	return `SELECT symphony_runs.id, symphony_runs.app_id, symphony_runs.task_id, symphony_runs.attempt, symphony_runs.status,
		symphony_runs.repo_root, symphony_runs.repo_workspace_path, symphony_runs.workspace_path,
		symphony_runs.thread_id, symphony_runs.turn_id, symphony_runs.process_id, symphony_runs.owner_session_id,
		coalesce(symphony_runs.owner_started_at, ''), coalesce(symphony_runs.lease_expires_at, ''),
		symphony_runs.summary, symphony_runs.error, symphony_runs.diff_stat, symphony_runs.diff,
		coalesce(symphony_runs.started_at, ''), coalesce(symphony_runs.ended_at, ''),
		symphony_runs.created_at, symphony_runs.updated_at
		FROM symphony_runs`
}

func scanRuns(rows *sql.Rows) ([]Run, error) {
	var out []Run
	for rows.Next() {
		var run Run
		if err := rows.Scan(
			&run.ID, &run.AppID, &run.TaskID, &run.Attempt, &run.Status, &run.RepoRoot, &run.RepoWorkspace, &run.WorkspacePath,
			&run.ThreadID, &run.TurnID, &run.ProcessID, &run.OwnerSessionID, &run.OwnerStartedAt, &run.LeaseExpiresAt, &run.Summary, &run.Error,
			&run.DiffStat, &run.Diff, &run.StartedAt, &run.EndedAt, &run.CreatedAt, &run.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func cleanAppID(appID string) (string, error) {
	appID = strings.TrimSpace(appID)
	if appID == "" {
		return "", errors.New("app id is required")
	}
	return appID, nil
}

func cleanStatusKeys(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		key := strings.TrimSpace(value)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	sort.Strings(out)
	return out
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

func runExistsTx(ctx context.Context, tx *sql.Tx, appID, runID string) error {
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM symphony_runs WHERE app_id = ? AND id = ?`, appID, runID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sql.ErrNoRows
		}
		return err
	}
	return nil
}

func noActiveRunTx(ctx context.Context, tx *sql.Tx, appID, taskID string) error {
	var active int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM symphony_runs WHERE app_id = ? AND task_id = ? AND status IN ('queued', 'running') LIMIT 1`, appID, taskID).Scan(&active)
	if err == nil {
		return errors.New("task already has an active run")
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}

func nextRunAttemptTx(ctx context.Context, tx *sql.Tx, appID, taskID string) (int, error) {
	var attempt int
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(attempt), 0) + 1 FROM symphony_runs WHERE app_id = ? AND task_id = ?`, appID, taskID).Scan(&attempt); err != nil {
		return 0, err
	}
	return attempt, nil
}

func appendRunEventTx(ctx context.Context, tx *sql.Tx, appID, runID, eventType string, payload any, now string) error {
	eventType = strings.TrimSpace(eventType)
	if eventType == "" {
		return errors.New("run event type is required")
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var seq int
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(seq), 0) + 1 FROM symphony_run_events WHERE app_id = ? AND run_id = ?`, appID, runID).Scan(&seq); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO symphony_run_events (app_id, run_id, seq, type, payload_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		appID, runID, seq, eventType, string(payloadJSON), now,
	)
	return err
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
