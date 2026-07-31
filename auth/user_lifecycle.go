package auth

import (
	"context"
	"strings"

	authdb "scenery.sh/auth/db/gen"
	"scenery.sh/errs"
)

const maxLifecycleReasonLength = 200

// DisableUser globally disables a standard-auth user and atomically revokes
// every active refresh session. Application code remains responsible for
// authorizing and auditing who may perform this operation.
func DisableUser(ctx context.Context, userID AuthUserID, reason string) error {
	id, err := lifecycleUserID(userID)
	if err != nil {
		return err
	}
	reason, err = lifecycleReason(reason)
	if err != nil {
		return err
	}
	svc, err := lifecycleService(ctx)
	if err != nil {
		return err
	}
	tx, query, err := svc.beginTx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := query.DisableUserByID(ctx, id); err != nil {
		if isNoRows(err) {
			return errs.B().Code(errs.NotFound).Msg("auth user not found").Err()
		}
		return err
	}
	if err := query.RevokeUserRefreshSessions(ctx, authdb.RevokeUserRefreshSessionsParams{
		UserID: id, RevokedReason: reason,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

// EnableUser re-enables a standard-auth user. It does not recreate sessions,
// memberships, or provider connections; the user must sign in again.
func EnableUser(ctx context.Context, userID AuthUserID) error {
	id, err := lifecycleUserID(userID)
	if err != nil {
		return err
	}
	svc, err := lifecycleService(ctx)
	if err != nil {
		return err
	}
	if _, err := svc.query.EnableUserByID(ctx, id); err != nil {
		if isNoRows(err) {
			return errs.B().Code(errs.NotFound).Msg("auth user not found").Err()
		}
		return err
	}
	return nil
}

// RevokeUserSessions revokes every active refresh session for one standard-
// auth user without changing the user's enabled state.
func RevokeUserSessions(ctx context.Context, userID AuthUserID, reason string) error {
	id, err := lifecycleUserID(userID)
	if err != nil {
		return err
	}
	reason, err = lifecycleReason(reason)
	if err != nil {
		return err
	}
	svc, err := lifecycleService(ctx)
	if err != nil {
		return err
	}
	if _, err := svc.query.GetUserByID(ctx, id); err != nil {
		if isNoRows(err) {
			return errs.B().Code(errs.NotFound).Msg("auth user not found").Err()
		}
		return err
	}
	return svc.query.RevokeUserRefreshSessions(ctx, authdb.RevokeUserRefreshSessionsParams{
		UserID: id, RevokedReason: reason,
	})
}

func lifecycleService(ctx context.Context) (*Service, error) {
	if !currentStandardConfig().Enabled {
		return nil, failedPrecondition("standard auth is not configured")
	}
	svc, err := standardAuthService(ctx)
	if err != nil || svc == nil {
		return nil, errs.B().Code(errs.FailedPrecondition).Msg("standard auth is unavailable").Cause(err).Err()
	}
	return svc, nil
}

func lifecycleUserID(userID AuthUserID) (authdb.UUID, error) {
	id, err := parseUUID(string(userID))
	if err != nil {
		return authdb.UUID{}, invalidArgument("valid auth user id is required")
	}
	return id, nil
}

func lifecycleReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > maxLifecycleReasonLength || strings.ContainsAny(reason, "\r\n\x00") {
		return "", invalidArgument("reason must be between 1 and 200 characters")
	}
	return reason, nil
}
