package auth

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"scenery.sh/errs"
)

type permissionCheckerFunc func(context.Context, *AuthData, ...string) (bool, error)

func (f permissionCheckerFunc) HasPermissions(ctx context.Context, data *AuthData, permissions ...string) (bool, error) {
	return f(ctx, data, permissions...)
}

func resetPermissionCheckerForTest(t *testing.T) {
	t.Helper()
	SetPermissionChecker(nil)
	t.Cleanup(func() { SetPermissionChecker(nil) })
}

func TestHasPermissionsGrantDenialBatchAndExactNames(t *testing.T) {
	resetPermissionCheckerForTest(t)
	data := &AuthData{
		UserID:          "11111111-1111-1111-1111-111111111111",
		TenantID:        "22222222-2222-2222-2222-222222222222",
		SessionID:       "session-exact",
		ActorUserID:     "33333333-3333-3333-3333-333333333333",
		ImpersonationID: "impersonation-exact",
	}
	wantNames := []string{"Fleet", "fleet.read", " padded "}
	var calls int
	SetPermissionChecker(permissionCheckerFunc(func(_ context.Context, gotData *AuthData, names ...string) (bool, error) {
		calls++
		if gotData != data {
			t.Fatalf("auth data = %#v, want same pointer %#v", gotData, data)
		}
		if !reflect.DeepEqual(names, wantNames) {
			t.Fatalf("permission names = %#v, want %#v", names, wantNames)
		}
		return true, nil
	}))

	ctx := WithContext(t.Context(), UID(data.UserID), data)
	allowed, err := HasPermissions(ctx, wantNames...)
	if err != nil || !allowed {
		t.Fatalf("grant = %v, %v; want true, nil", allowed, err)
	}
	if calls != 1 {
		t.Fatalf("checker calls = %d, want 1", calls)
	}

	SetPermissionChecker(permissionCheckerFunc(func(context.Context, *AuthData, ...string) (bool, error) {
		return false, nil
	}))
	allowed, err = HasPermissions(ctx, "Fleet")
	if err != nil || allowed {
		t.Fatalf("denial = %v, %v; want false, nil", allowed, err)
	}
}

func TestHasPermissionsFailsClosed(t *testing.T) {
	resetPermissionCheckerForTest(t)
	data := &AuthData{UserID: "11111111-1111-1111-1111-111111111111"}
	authCtx := WithContext(t.Context(), UID(data.UserID), data)

	tests := []struct {
		name        string
		ctx         context.Context
		permissions []string
		wantCode    errs.ErrCode
	}{
		{name: "no permissions", ctx: authCtx, wantCode: errs.InvalidArgument},
		{name: "empty permission", ctx: authCtx, permissions: []string{""}, wantCode: errs.InvalidArgument},
		{name: "whitespace permission", ctx: authCtx, permissions: []string{" \t"}, wantCode: errs.InvalidArgument},
		{name: "missing auth", ctx: t.Context(), permissions: []string{"Fleet"}, wantCode: errs.Unauthenticated},
		{name: "missing checker", ctx: authCtx, permissions: []string{"Fleet"}, wantCode: errs.FailedPrecondition},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			allowed, err := HasPermissions(test.ctx, test.permissions...)
			if allowed || errs.Code(err) != test.wantCode {
				t.Fatalf("HasPermissions = %v, %v (code %q), want false, %q", allowed, err, errs.Code(err), test.wantCode)
			}
		})
	}
}

func TestHasPermissionsPropagatesCheckerError(t *testing.T) {
	resetPermissionCheckerForTest(t)
	want := errors.New("permission store unavailable")
	SetPermissionChecker(permissionCheckerFunc(func(context.Context, *AuthData, ...string) (bool, error) {
		return false, want
	}))
	data := &AuthData{UserID: "11111111-1111-1111-1111-111111111111"}
	allowed, err := HasPermissions(WithContext(t.Context(), UID(data.UserID), data), "Fleet")
	if allowed || !errors.Is(err, want) {
		t.Fatalf("HasPermissions = %v, %v; want false, original checker error", allowed, err)
	}
}

func TestSetPermissionCheckerReplacesResetsAndIsConcurrentSafe(t *testing.T) {
	resetPermissionCheckerForTest(t)
	data := &AuthData{UserID: "11111111-1111-1111-1111-111111111111"}
	grant := permissionCheckerFunc(func(context.Context, *AuthData, ...string) (bool, error) { return true, nil })
	deny := permissionCheckerFunc(func(context.Context, *AuthData, ...string) (bool, error) { return false, nil })

	SetPermissionChecker(grant)
	if allowed, err := hasPermissions(t.Context(), data, true, "Fleet"); err != nil || !allowed {
		t.Fatalf("grant checker = %v, %v", allowed, err)
	}
	SetPermissionChecker(deny)
	if allowed, err := hasPermissions(t.Context(), data, true, "Fleet"); err != nil || allowed {
		t.Fatalf("replacement checker = %v, %v", allowed, err)
	}
	SetPermissionChecker(nil)
	if _, err := hasPermissions(t.Context(), data, true, "Fleet"); errs.Code(err) != errs.FailedPrecondition {
		t.Fatalf("reset checker error = %v (code %q), want failed_precondition", err, errs.Code(err))
	}

	var wait sync.WaitGroup
	for range 20 {
		wait.Add(2)
		go func() {
			defer wait.Done()
			for range 100 {
				SetPermissionChecker(grant)
				SetPermissionChecker(deny)
				SetPermissionChecker(nil)
			}
		}()
		go func() {
			defer wait.Done()
			for range 100 {
				_, _ = hasPermissions(t.Context(), data, true, "Fleet")
			}
		}()
	}
	wait.Wait()
}
