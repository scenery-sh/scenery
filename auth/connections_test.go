package auth

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"scenery.sh/errs"
	"scenery.sh/internal/appsdk"
)

type connectionTestHost struct {
	appsdk.Host
	token func(context.Context, string, string, []string) (string, error)
}

func (h connectionTestHost) CurrentAuth() (appsdk.Auth, bool) { return appsdk.Auth{}, false }
func (h connectionTestHost) ConnectionToken(ctx context.Context, user, provider string, scopes []string) (string, error) {
	return h.token(ctx, user, provider, scopes)
}

func TestConnectionTokenRequestAndWorkerAuthority(t *testing.T) {
	const owner = "aaaaaaaa-0000-0000-0000-000000000001"
	previous := appsdk.CurrentHost()
	t.Cleanup(func() { appsdk.RegisterHost(previous) })
	calls := 0
	appsdk.RegisterHost(connectionTestHost{token: func(ctx context.Context, user, provider string, scopes []string) (string, error) {
		calls++
		if user != owner || provider != "files" || !reflect.DeepEqual(scopes, []string{"Read"}) {
			t.Fatal("SDK changed owner, provider or opaque scopes")
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return "private-test-token", nil
	}})
	ctx := context.WithValue(t.Context(), authDataContextKey{}, &AuthData{UserID: owner})
	if token, err := ConnectionToken(ctx, "files", "Read"); err != nil || token != "private-test-token" {
		t.Fatalf("request token: %v", err)
	}
	if _, err := ConnectionTokenForUser(t.Context(), owner, "files", "Read"); err != nil {
		t.Fatalf("worker token: %v", err)
	}
	if _, err := ConnectionTokenForUser(ctx, "00000000-0000-0000-0000-000000000002", "files", "Read"); errs.Code(err) != errs.PermissionDenied {
		t.Fatalf("cross-user request: %v", err)
	}
	if _, err := ConnectionToken(t.Context(), "files"); errs.Code(err) != errs.Unauthenticated {
		t.Fatalf("anonymous request: %v", err)
	}
	if _, err := ConnectionTokenForUser(ctx, strings.ToUpper(owner), "files", "Read"); err != nil {
		t.Fatalf("logical UUID owner: %v", err)
	}
	if calls != 3 {
		t.Fatalf("unauthorized request reached host: %d", calls)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ConnectionTokenForUser(cancelled, owner, "files", "Read"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	appsdk.RegisterHost(nil)
	if _, err := ConnectionTokenForUser(t.Context(), owner, "files"); errs.Code(err) != errs.ConnectionNotConfigured {
		t.Fatalf("unlinked SDK: %v", err)
	}
}
