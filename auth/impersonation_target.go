package auth

import (
	"context"
	"database/sql"
	"strings"

	authdb "scenery.sh/auth/db/gen"
	"scenery.sh/errs"
)

// PrepareImpersonationTargetParams identifies an application-owned person who
// should be available to a privileged actor as an impersonation target. UserID
// is optional for the first preparation and authoritative when provided.
type PrepareImpersonationTargetParams struct {
	UserID      AuthUserID
	Email       string
	DisplayName string
}

// PrepareImpersonationTarget resolves or creates a provider-free auth user and
// ensures that user is an active member of the actor's current tenant. It does
// not verify the email, create a login identity, or issue a session.
func PrepareImpersonationTarget(ctx context.Context, params PrepareImpersonationTargetParams) (UserProfile, error) {
	data, ok := currentAuthDataFromContext(ctx)
	if !ok || data == nil {
		return UserProfile{}, unauthenticated("authentication required")
	}
	if data.Impersonating() {
		return UserProfile{}, permissionDenied("nested impersonation is not allowed")
	}
	actorID, err := parseUUID(string(data.UserID))
	if err != nil {
		return UserProfile{}, unauthenticated("invalid actor user id")
	}
	tenantID, err := parseUUID(string(data.TenantID))
	if err != nil {
		return UserProfile{}, unauthenticated("invalid tenant id")
	}
	normalizedEmail, err := normalizeEmail(params.Email)
	if err != nil {
		return UserProfile{}, invalidArgument(err.Error())
	}

	svc, err := lifecycleService(ctx)
	if err != nil {
		return UserProfile{}, err
	}
	tx, q, err := svc.beginTx(ctx)
	if err != nil {
		return UserProfile{}, err
	}
	defer func() { _ = tx.Rollback() }()

	actor, err := q.GetUserByID(ctx, actorID)
	if err != nil {
		return UserProfile{}, err
	}
	if actor.DisabledAt.Valid || !actor.CanImpersonateUsers {
		return UserProfile{}, permissionDenied("platform impersonation permission is required")
	}
	if _, err := q.GetActiveMembership(ctx, authdb.GetActiveMembershipParams{UserID: actor.ID, TenantID: tenantID}); err != nil {
		if isNoRows(err) {
			return UserProfile{}, permissionDenied("actor is not an active member of the current tenant")
		}
		return UserProfile{}, err
	}

	target, err := resolveImpersonationTarget(ctx, q, params, normalizedEmail)
	if err != nil {
		return UserProfile{}, err
	}
	if uuidString(target.ID) == uuidString(actor.ID) {
		return UserProfile{}, invalidArgument("cannot impersonate the current user")
	}
	if target.DisabledAt.Valid {
		return UserProfile{}, permissionDenied("target user is disabled")
	}
	if _, err := q.GetActiveMembership(ctx, authdb.GetActiveMembershipParams{UserID: target.ID, TenantID: tenantID}); err != nil {
		if !isNoRows(err) {
			return UserProfile{}, err
		}
		membershipID, idErr := newUUID()
		if idErr != nil {
			return UserProfile{}, idErr
		}
		if _, err := q.CreateOrganizationMembership(ctx, authdb.CreateOrganizationMembershipParams{
			ID: membershipID, TenantID: tenantID, UserID: target.ID, Role: roleMember,
			InvitedByUserID: actor.ID, InvitedAt: sql.NullTime{Time: svc.clock(), Valid: true},
		}); err != nil {
			return UserProfile{}, err
		}
	}

	svc.recordEvent(ctx, q, "impersonation_target_prepared", target.ID, actor.ID, tenantID, authdb.UUID{}, nil)
	if err := tx.Commit(); err != nil {
		return UserProfile{}, err
	}
	return mapUser(target), nil
}

func resolveImpersonationTarget(ctx context.Context, q authdb.Querier, params PrepareImpersonationTargetParams, normalizedEmail string) (authdb.ScenerySceneryAuthUser, error) {
	if strings.TrimSpace(string(params.UserID)) != "" {
		id, err := lifecycleUserID(params.UserID)
		if err != nil {
			return authdb.ScenerySceneryAuthUser{}, err
		}
		user, err := q.GetUserByID(ctx, id)
		if err != nil {
			if isNoRows(err) {
				return authdb.ScenerySceneryAuthUser{}, errs.B().Code(errs.NotFound).Msg("auth user not found").Err()
			}
			return authdb.ScenerySceneryAuthUser{}, err
		}
		if user.NormalizedPrimaryEmail != normalizedEmail {
			return authdb.ScenerySceneryAuthUser{}, errs.B().Code(errs.Conflict).Msg("auth user email does not match the business user").Err()
		}
		return user, nil
	}

	user, err := q.GetUserByNormalizedEmail(ctx, normalizedEmail)
	if err == nil {
		return user, nil
	}
	if !isNoRows(err) {
		return authdb.ScenerySceneryAuthUser{}, err
	}
	userID, err := newUUID()
	if err != nil {
		return authdb.ScenerySceneryAuthUser{}, err
	}
	return q.CreateUser(ctx, authdb.CreateUserParams{
		ID: userID, DisplayName: defaultDisplayName(normalizedEmail, params.DisplayName),
		PrimaryEmail: displayEmail(params.Email), NormalizedPrimaryEmail: normalizedEmail,
	})
}
