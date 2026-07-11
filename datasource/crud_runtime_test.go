package datasource

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
)

type crudDriverQuery struct {
	SQL  string
	Args []driver.NamedValue
}

type crudDriverState struct {
	queries   []crudDriverQuery
	responses [][][]driver.Value
}

type crudTestDriver struct{ state *crudDriverState }
type crudTestConn struct{ state *crudDriverState }
type crudTestRows struct {
	values [][]driver.Value
	index  int
}

var crudDriverSequence atomic.Uint64

func (driverInstance *crudTestDriver) Open(string) (driver.Conn, error) {
	return &crudTestConn{state: driverInstance.state}, nil
}

func (connection *crudTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not used by CRUD tests")
}
func (connection *crudTestConn) Close() error { return nil }
func (connection *crudTestConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not used by CRUD tests")
}
func (connection *crudTestConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	connection.state.queries = append(connection.state.queries, crudDriverQuery{SQL: query, Args: append([]driver.NamedValue(nil), args...)})
	var response [][]driver.Value
	if len(connection.state.responses) > 0 {
		response = connection.state.responses[0]
		connection.state.responses = connection.state.responses[1:]
	}
	return &crudTestRows{values: response}, nil
}

func (rows *crudTestRows) Columns() []string { return []string{"id", "tenant_id", "name"} }
func (rows *crudTestRows) Close() error      { return nil }
func (rows *crudTestRows) Next(destination []driver.Value) error {
	if rows.index >= len(rows.values) {
		return io.EOF
	}
	copy(destination, rows.values[rows.index])
	rows.index++
	return nil
}

func openCRUDTestDatabase(t *testing.T, responses [][][]driver.Value) (*sql.DB, *crudDriverState) {
	t.Helper()
	state := &crudDriverState{responses: responses}
	name := fmt.Sprintf("scenery-crud-test-%d", crudDriverSequence.Add(1))
	sql.Register(name, &crudTestDriver{state: state})
	database, err := sql.Open(name, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database, state
}

func TestInvokeCRUDExecutesTenantScopedSQLAcrossEveryAction(t *testing.T) {
	row := []driver.Value{"scene-1", "tenant-1", "roof"}
	database, state := openCRUDTestDatabase(t, [][][]driver.Value{{row}, {row}, {row}, {row}, {row}})
	spec := CRUDSpec{Address: "house/crud/scenes", Relation: "scenes", Fields: []CRUDField{
		{Name: "id", Column: "id", Type: "uuid", PrimaryKey: true, DefaultStrategy: "uuid_v7"},
		{Name: "tenant_id", Column: "tenant_id", Type: "string", TenantKey: true, Immutable: true},
		{Name: "name", Column: "name", Type: "string"},
	}}
	cases := []struct {
		action string
		input  string
	}{
		{"list", `{"tenant_id":"tenant-1"}`},
		{"get", `{"id":"scene-1","tenant_id":"tenant-1"}`},
		{"create", `{"tenant_id":"tenant-1","name":"roof"}`},
		{"update", `{"id":"scene-1","tenant_id":"tenant-1","name":"roof"}`},
		{"delete", `{"id":"scene-1","tenant_id":"tenant-1"}`},
	}
	for _, test := range cases {
		output, err := InvokeCRUD(context.Background(), database, spec, test.action, []byte(test.input))
		if err != nil {
			t.Fatalf("%s: %v", test.action, err)
		}
		if !strings.Contains(string(output), `"tenant_id":"tenant-1"`) {
			t.Fatalf("%s output = %s", test.action, output)
		}
	}
	if len(state.queries) != len(cases) {
		t.Fatalf("queries = %#v", state.queries)
	}
	wants := []string{
		`SELECT "id", "tenant_id", "name" FROM "scenes" WHERE "tenant_id" = $1 ORDER BY "id"`,
		`SELECT "id", "tenant_id", "name" FROM "scenes" WHERE "id" = $1 AND "tenant_id" = $2 LIMIT 1`,
		`INSERT INTO "scenes" ("id", "tenant_id", "name") VALUES ($1, $2, $3) RETURNING "id", "tenant_id", "name"`,
		`UPDATE "scenes" SET "name" = $1 WHERE "id" = $2 AND "tenant_id" = $3 RETURNING "id", "tenant_id", "name"`,
		`DELETE FROM "scenes" WHERE "id" = $1 AND "tenant_id" = $2 RETURNING "id", "tenant_id", "name"`,
	}
	for index, want := range wants {
		if state.queries[index].SQL != want {
			t.Errorf("query %d = %q, want %q", index, state.queries[index].SQL, want)
		}
	}
}

func TestInvokeCRUDMapsEmptyGetToNotFound(t *testing.T) {
	database, _ := openCRUDTestDatabase(t, [][][]driver.Value{{}})
	spec := CRUDSpec{Address: "house/crud/scenes", Relation: "scenes", Fields: []CRUDField{
		{Name: "id", Column: "id", PrimaryKey: true},
		{Name: "tenant_id", Column: "tenant_id", TenantKey: true},
		{Name: "name", Column: "name"},
	}}
	_, err := InvokeCRUD(context.Background(), database, spec, "get", []byte(`{"id":"missing","tenant_id":"tenant-1"}`))
	if !errors.Is(err, ErrCRUDNotFound) {
		t.Fatalf("get error = %v", err)
	}
}

var _ driver.QueryerContext = (*crudTestConn)(nil)
