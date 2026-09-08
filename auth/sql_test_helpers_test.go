package auth

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	authdb "scenery.sh/auth/db/gen"
)

// Script only the SQL boundary needed by a decision test. Real PostgreSQL
// semantics, durable state and concurrent row locking are release-probe proof.
type authSQLStep struct {
	name  string
	rows  [][]driver.Value
	err   error
	check func(string, []driver.NamedValue)
}

type authSQLScript struct {
	steps     []authSQLStep
	events    []string
	exec      func(string, []driver.NamedValue) error
	commitErr error
}

type authSQLConn struct{ script *authSQLScript }
type authSQLTx struct{ script *authSQLScript }
type authSQLRows struct {
	values  [][]driver.Value
	columns []string
}

var authSQLNow = time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

const authSQLUserID = "22222222-2222-2222-2222-222222222222"
const authSQLTenantID = "33333333-3333-3333-3333-333333333333"

func authSQLTenantStep() authSQLStep {
	return authSQLStep{name: "EnsureDevBootstrapTenant", rows: [][]driver.Value{{authSQLTenantID, "Workspace", nil, authSQLNow, authSQLNow}}}
}

func authSQLMembershipRow() []driver.Value {
	return []driver.Value{"11111111-1111-1111-1111-111111111111", authSQLTenantID, authSQLUserID, roleOwner, nil, nil, nil, authSQLNow, authSQLNow}
}

func authSQLMembershipStep(t *testing.T) authSQLStep {
	t.Helper()
	return authSQLStep{name: "CreateOrganizationMembership", rows: [][]driver.Value{authSQLMembershipRow()}, check: func(_ string, args []driver.NamedValue) {
		if args[1].Value != authSQLTenantID || args[2].Value != authSQLUserID || args[3].Value != roleOwner {
			t.Fatalf("membership args=%v", args)
		}
	}}
}

func authSQLService(t *testing.T, steps ...authSQLStep) (*Service, *authSQLScript) {
	t.Helper()
	script := &authSQLScript{steps: steps}
	db := sql.OpenDB(script)
	t.Cleanup(func() {
		_ = db.Close()
		if len(script.steps) != 0 {
			t.Errorf("unused SQL steps: %+v", script.steps)
		}
	})
	return &Service{db: db, query: authdb.New(db), now: func() time.Time { return authSQLNow }}, script
}

func (s *authSQLScript) Connect(context.Context) (driver.Conn, error) { return &authSQLConn{s}, nil }
func (s *authSQLScript) Driver() driver.Driver                        { return s }
func (s *authSQLScript) Open(string) (driver.Conn, error)             { return &authSQLConn{s}, nil }
func (c *authSQLConn) Close() error                                   { return nil }
func (c *authSQLConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("auth SQL test does not prepare statements")
}
func (c *authSQLConn) Begin() (driver.Tx, error) {
	c.script.events = append(c.script.events, "begin")
	return &authSQLTx{c.script}, nil
}
func (t *authSQLTx) Commit() error {
	t.script.events = append(t.script.events, "commit")
	return t.script.commitErr
}
func (t *authSQLTx) Rollback() error {
	t.script.events = append(t.script.events, "rollback")
	return nil
}

func (s *authSQLScript) next(query string, args []driver.NamedValue) (authSQLStep, error) {
	if len(s.steps) == 0 {
		return authSQLStep{}, fmt.Errorf("unexpected SQL: %s", query)
	}
	step := s.steps[0]
	s.steps = s.steps[1:]
	if !strings.HasPrefix(query, "-- name: "+step.name+" ") {
		return step, fmt.Errorf("SQL=%q, want %s", query, step.name)
	}
	s.events = append(s.events, step.name)
	if step.check != nil {
		step.check(query, args)
	}
	return step, step.err
}

func (c *authSQLConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	step, err := c.script.next(query, args)
	if err != nil {
		return nil, err
	}
	width := 1
	if len(step.rows) > 0 {
		width = len(step.rows[0])
	}
	columns := make([]string, width)
	for index := range columns {
		columns[index] = fmt.Sprintf("column_%d", index)
	}
	return &authSQLRows{values: step.rows, columns: columns}, nil
}

func (c *authSQLConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if c.script.exec != nil {
		return driver.RowsAffected(1), c.script.exec(query, args)
	}
	_, err := c.script.next(query, args)
	return driver.RowsAffected(1), err
}

func (r *authSQLRows) Columns() []string { return r.columns }
func (r *authSQLRows) Close() error      { return nil }
func (r *authSQLRows) Next(values []driver.Value) error {
	if len(r.values) == 0 {
		return io.EOF
	}
	copy(values, r.values[0])
	r.values = r.values[1:]
	return nil
}

func authSQLUserRow(user authdb.SceneryAuthUser) []driver.Value {
	var verified, disabled driver.Value
	if user.EmailVerifiedAt.Valid {
		verified = user.EmailVerifiedAt.Time
	}
	if user.DisabledAt.Valid {
		disabled = user.DisabledAt.Time
	}
	return []driver.Value{uuidString(user.ID), user.DisplayName, user.AvatarUrl, user.PrimaryEmail, user.NormalizedPrimaryEmail, verified, disabled, user.CanImpersonateUsers, authSQLNow, authSQLNow}
}

func authSQLSessionRow(tokenHash string) []driver.Value {
	return []driver.Value{"11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222", tokenHash, "", nil, "33333333-3333-3333-3333-333333333333", authSQLNow.Add(time.Hour), nil, nil, "", "", "", nil, nil, "", authSQLNow, authSQLNow}
}
