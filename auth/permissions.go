package auth

import (
	"context"
	"strings"
	"sync"
)

// PermissionChecker resolves application-owned permission names for the
// current standard-auth identity. It must return true only when the user has
// every requested permission. Implementations should be read-only.
type PermissionChecker interface {
	HasPermissions(context.Context, *AuthData, ...string) (bool, error)
}

var permissionCheckerState struct {
	mu      sync.RWMutex
	checker PermissionChecker
}

// SetPermissionChecker configures the application's permission checker.
// Applications should call it during startup, before runtime.Main begins
// serving requests. Calling it again replaces the checker; nil clears it.
func SetPermissionChecker(checker PermissionChecker) {
	permissionCheckerState.mu.Lock()
	permissionCheckerState.checker = checker
	permissionCheckerState.mu.Unlock()
}

// HasPermissions reports whether the current standard-auth user has every
// requested application-owned permission. Names are passed to the checker
// once, exactly as supplied, and are case-sensitive.
func HasPermissions(ctx context.Context, permissions ...string) (bool, error) {
	data, ok := currentAuthDataFromContext(ctx)
	return hasPermissions(ctx, data, ok, permissions...)
}

func hasPermissions(ctx context.Context, data *AuthData, authenticated bool, permissions ...string) (bool, error) {
	if len(permissions) == 0 {
		return false, invalidArgument("at least one permission is required")
	}
	for _, permission := range permissions {
		if strings.TrimSpace(permission) == "" {
			return false, invalidArgument("permission names must not be empty")
		}
	}
	if !authenticated || data == nil {
		return false, unauthenticated("permission check requires auth")
	}

	permissionCheckerState.mu.RLock()
	checker := permissionCheckerState.checker
	permissionCheckerState.mu.RUnlock()
	if checker == nil {
		return false, failedPrecondition("application permission checker is not configured")
	}
	return checker.HasPermissions(ctx, data, permissions...)
}
