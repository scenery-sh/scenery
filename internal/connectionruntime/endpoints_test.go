package connectionruntime

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"scenery.sh/internal/connectionvault"
)

type callbackSQL struct {
	err     error
	queries int
}

func (s *callbackSQL) Connect(context.Context) (driver.Conn, error) { return s, nil }
func (s *callbackSQL) Driver() driver.Driver                        { return s }
func (s *callbackSQL) Open(string) (driver.Conn, error)             { return s, nil }
func (*callbackSQL) Close() error                                   { return nil }
func (*callbackSQL) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (*callbackSQL) Begin() (driver.Tx, error) { return nil, errors.New("unexpected transaction") }
func (s *callbackSQL) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if !strings.HasPrefix(query, "-- name: ConsumeConnectionState ") {
		return nil, errors.New("unexpected query")
	}
	s.queries++
	return nil, s.err
}

func TestCallbackDistinguishesStorageFailureFromInvalidState(t *testing.T) {
	for _, scenario := range []struct {
		err    error
		status int
		code   string
	}{
		{sql.ErrNoRows, 400, "invalid_argument"},
		{fmt.Errorf("state: %w", sql.ErrNoRows), 400, "invalid_argument"},
		{context.DeadlineExceeded, 503, "connection_unavailable"},
		{errors.New("private database error details"), 503, "connection_unavailable"},
	} {
		boundary := &callbackSQL{err: scenario.err}
		pool := sql.OpenDB(boundary)
		s := &Service{options: Options{Open: func(context.Context) (*sql.DB, error) { return pool, nil }}, providers: map[string]configuredProvider{"files": {callbackURI: "https://app.test/auth/connections/files/callback", registration: connectionvault.Registration{Issuer: "https://issuer.test"}}}}
		if err := s.schema.ensure(t.Context(), pool, func() error { return nil }); err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodGet, "https://app.test/auth/connections/files/callback?state=pending", nil)
		request.AddCookie(browserCookie("files", s.providers["files"].callbackURI, "browser", false))
		w := httptest.NewRecorder()
		s.callback(w, request, "files")
		response := w.Result()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		_ = pool.Close()
		if w.Code != scenario.status || !strings.Contains(string(body), scenario.code) || strings.Contains(string(body), "private database error details") || len(response.Cookies()) != 0 || boundary.queries != 1 {
			t.Fatalf("callback failure: status=%d body=%s queries=%d", w.Code, body, boundary.queries)
		}
	}
}
