package auth

import (
	"context"

	authdb "scenery.sh/auth/db/gen"
	"scenery.sh/errs"
)

// CurrentUser returns the live standard-auth profile for the authenticated
// user. It reads the framework-owned user row without rotating sessions,
// creating tenants, or issuing tokens.
func CurrentUser(ctx context.Context) (UserProfile, error) {
	data, ok := currentAuthDataFromContext(ctx)
	if !ok {
		return UserProfile{}, unauthenticated("current user requires auth")
	}
	userID, err := parseUUID(string(data.UserID))
	if err != nil {
		return UserProfile{}, unauthenticated("invalid user id")
	}
	if !currentStandardConfig().Enabled {
		return UserProfile{}, failedPrecondition("standard auth is not configured")
	}
	svc, err := standardAuthService(ctx)
	if err != nil {
		return UserProfile{}, errs.B().Code(errs.FailedPrecondition).Msg("standard auth is unavailable").Cause(err).Err()
	}
	return currentUser(ctx, svc, userID)
}

func currentUser(ctx context.Context, svc *Service, userID authdb.UUID) (UserProfile, error) {
	user, err := svc.query.GetUserByID(ctx, userID)
	if err != nil {
		if isNoRows(err) {
			return UserProfile{}, unauthenticated("authenticated user is unavailable")
		}
		return UserProfile{}, err
	}
	if user.DisabledAt.Valid {
		return UserProfile{}, permissionDenied("user is disabled")
	}
	return mapUser(user), nil
}
