package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"
)

func TestRunDueSchedulesReturnsRowIterationError(t *testing.T) {
	t.Parallel()
	failure := errors.New("schedule stream interrupted")
	rows := &failedScheduleRows{err: failure}
	db := sql.OpenDB(scheduleConnector{rows: rows})
	defer func() { _ = db.Close() }()
	s := &Store{db: db, Service: "test"}
	jobs, err := s.RunDueSchedules(t.Context(), time.Now())
	if !errors.Is(err, failure) || len(jobs) != 0 || !rows.closed {
		t.Fatalf("jobs=%v err=%v rows closed=%v", jobs, err, rows.closed)
	}
}

type scheduleConnector struct{ rows *failedScheduleRows }

func (c scheduleConnector) Connect(context.Context) (driver.Conn, error) {
	return scheduleConnection(c), nil
}
func (scheduleConnector) Driver() driver.Driver { return nil }

type scheduleConnection struct{ rows *failedScheduleRows }

func (scheduleConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (scheduleConnection) Begin() (driver.Tx, error) {
	return nil, errors.New("unexpected transaction")
}
func (scheduleConnection) Close() error { return nil }
func (c scheduleConnection) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return c.rows, nil
}

type failedScheduleRows struct {
	err    error
	closed bool
}

func (*failedScheduleRows) Columns() []string {
	return []string{"id", "task_name", "catchup_window_ms", "input_blob"}
}
func (r *failedScheduleRows) Next([]driver.Value) error { return r.err }
func (r *failedScheduleRows) Close() error {
	r.closed = true
	return nil
}
