package auth_test

import (
	"context"

	"scenery.sh/auth"
)

type appPermissions struct{}

func (appPermissions) HasPermissions(_ context.Context, data *auth.AuthData, permissions ...string) (bool, error) {
	// Query application-owned permission data with data.UserID. Return true
	// only when the user has every requested permission.
	return data != nil && len(permissions) > 0, nil
}

func ExampleSetPermissionChecker() {
	auth.SetPermissionChecker(appPermissions{})
	defer auth.SetPermissionChecker(nil)
}
