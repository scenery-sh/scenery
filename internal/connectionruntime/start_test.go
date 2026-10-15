package connectionruntime

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"scenery.sh/errs"
	"scenery.sh/internal/appsdk"
	"scenery.sh/internal/connectionapi"
)

type startHost struct {
	configurationTestHost
	endpoints map[string]appsdk.Endpoint
}

func (h *startHost) RegisterEndpoint(endpoint appsdk.Endpoint) { h.endpoints[endpoint.Name] = endpoint }
func (*startHost) DecodeJSON(req *http.Request, value any) error {
	return json.NewDecoder(req.Body).Decode(value)
}

func TestConnectionStartRequiresCallbackHostname(t *testing.T) {
	host := &startHost{configurationTestHost: configurationTestHost{callback: "https://api.example.test/auth/connections/google/callback"}, endpoints: map[string]appsdk.Endpoint{}}
	service, err := New(Options{Host: host, Providers: []connectionapi.ProviderConfig{{ID: "google", Preset: "google"}}})
	if err != nil {
		t.Fatal(err)
	}
	service.Register()
	for _, scenario := range []struct {
		host    string
		allowed bool
	}{
		{"api.example.test", true}, {"API.example.test:443", true}, {"api.example.test:8443", true},
		{"other.example.test", false}, {"api.example.test.attacker.test", false},
	} {
		request := httptest.NewRequest(http.MethodPost, "https://"+scenario.host+"/auth/connections/google/start", strings.NewReader(`{}`))
		request.Header.Set("X-Forwarded-Host", "api.example.test")
		_, _, err := host.endpoints["ConnectionStart"].Decode(request, map[string]string{"provider": "google"})
		if (err == nil) != scenario.allowed || err != nil && errs.Code(err) != errs.InvalidArgument {
			t.Fatalf("host %s: %v", scenario.host, err)
		}
	}
}

const startOwner = "11111111-1111-4111-8111-111111111111"
const startSession = "22222222-2222-4222-8222-222222222222"

var startNow = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

type startSQL struct {
	callbackSQL
	stage, invalid string
}

func (s *startSQL) Connect(context.Context) (driver.Conn, error) { return s, nil }
func (s *startSQL) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	s.queries++
	stage := "session"
	if strings.HasPrefix(query, "-- name: GetUserByID ") {
		stage = "owner"
	} else if !strings.HasPrefix(query, "-- name: GetRefreshSessionByID ") {
		return nil, errors.New("unexpected query")
	}
	if stage == s.stage {
		return nil, s.err
	}
	columns := strings.Split(strings.Split(strings.Split(query, "\nSELECT ")[1], "\nFROM")[0], ", ")
	values := map[string]driver.Value{"id": startSession, "user_id": startOwner, "expires_at": startNow.Add(time.Hour), "rotated_at": startNow, "created_at": startNow, "updated_at": startNow, "previous_token_expires_at": nil, "active_tenant_id": nil, "actor_user_id": nil, "impersonation_id": nil, "revoked_at": nil}
	switch s.invalid {
	case "expired":
		values["expires_at"] = startNow
	case "revoked":
		values["revoked_at"] = startNow
	case "wrong_owner":
		values["user_id"] = startSession
	}
	row := make([]driver.Value, len(columns))
	for i, name := range columns {
		if value, ok := values[name]; ok {
			row[i] = value
		} else {
			row[i] = ""
		}
	}
	return &startRows{columns: columns, values: row}, nil
}

type startRows struct {
	columns []string
	values  []driver.Value
}

func (r *startRows) Columns() []string { return r.columns }
func (*startRows) Close() error        { return nil }
func (r *startRows) Next(dest []driver.Value) error {
	if r.values == nil {
		return io.EOF
	}
	copy(dest, r.values)
	r.values = nil
	return nil
}

func TestConnectionStartDistinguishesStorageFailureFromInvalidSession(t *testing.T) {
	for _, scenario := range []struct {
		stage, invalid string
		err            error
		code           errs.ErrCode
	}{
		{"session", "", sql.ErrNoRows, errs.Unauthenticated},
		{"session", "", context.DeadlineExceeded, errs.ConnectionUnavailable},
		{"session", "", errors.New("private session storage failure"), errs.ConnectionUnavailable},
		{"owner", "", sql.ErrNoRows, errs.PermissionDenied},
		{"owner", "", context.DeadlineExceeded, errs.ConnectionUnavailable},
		{"owner", "", errors.New("private owner storage failure"), errs.ConnectionUnavailable},
		{"", "expired", nil, errs.Unauthenticated}, {"", "revoked", nil, errs.Unauthenticated}, {"", "wrong_owner", nil, errs.Unauthenticated},
	} {
		boundary := &startSQL{callbackSQL: callbackSQL{err: scenario.err}, stage: scenario.stage, invalid: scenario.invalid}
		pool := sql.OpenDB(boundary)
		service, err := New(Options{Host: configurationTestHost{callback: "https://api.example.test/auth/connections/google/callback"}, Providers: []connectionapi.ProviderConfig{{ID: "google", Preset: "google"}}, Open: func(context.Context) (*sql.DB, error) { return pool, nil }, Principal: func(context.Context) (string, string, error) { return startOwner, startSession, nil }, Now: func() time.Time { return startNow }})
		if err != nil {
			t.Fatal(err)
		}
		if err := service.schema.ensure(t.Context(), pool, func() error { return nil }); err != nil {
			t.Fatal(err)
		}
		result, err := service.start(t.Context(), "google", &connectionapi.StartParams{})
		_ = pool.Close()
		queries := 1
		if scenario.stage == "owner" {
			queries = 2
		}
		if result != nil || errs.Code(err) != scenario.code || strings.Contains(err.Error(), "private") || boundary.queries != queries {
			t.Fatalf("%s/%s: result=%#v err=%v queries=%d", scenario.stage, scenario.invalid, result, err, boundary.queries)
		}
	}
}
