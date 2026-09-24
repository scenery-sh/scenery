package auth

import (
	"context"
	"strings"

	authdb "scenery.sh/auth/db/gen"
	"scenery.sh/errs"
)

// Organization roles of standard-auth memberships. Standard auth enforces
// them for its own organization endpoints (only owners invite, change roles
// and disable members); an application reads them through CurrentMembership
// and MembershipOf when it applies the same rule to its own data.
const (
	RoleOwner  = "owner"
	RoleMember = "member"
)

// Membership is an active standard-auth membership of a user in a tenant.
type Membership struct {
	UserID   AuthUserID
	TenantID TenantID
	// Role is RoleOwner or RoleMember.
	Role string
}

// CurrentMembership returns the authenticated user's active membership in the
// request's tenant, read from standard auth's own records. During
// impersonation it is the effective user's membership. A request without an
// authenticated tenant returns unauthenticated; a disabled user or a
// membership that is no longer active returns permission denied.
func CurrentMembership(ctx context.Context) (Membership, error) {
	svc, caller, tenant, err := membershipScope(ctx)
	if err != nil {
		return Membership{}, err
	}
	return svc.activeMembership(ctx, caller, tenant, permissionDenied("workspace access is disabled"))
}

// MembershipOf returns the active membership of userID in the authenticated
// request's tenant. The caller must be an active member of that tenant
// itself. A user who is unknown, disabled or not an active member there
// returns not found, so a tenant cannot probe users of other tenants.
func MembershipOf(ctx context.Context, userID AuthUserID) (Membership, error) {
	svc, caller, tenant, err := membershipScope(ctx)
	if err != nil {
		return Membership{}, err
	}
	return svc.membershipOf(ctx, caller, tenant, userID)
}

func (s *Service) membershipOf(ctx context.Context, caller, tenant authdb.UUID, userID AuthUserID) (Membership, error) {
	if _, err := s.activeMembership(ctx, caller, tenant, permissionDenied("workspace access is disabled")); err != nil {
		return Membership{}, err
	}
	target, err := parseUUID(strings.TrimSpace(string(userID)))
	if err != nil {
		return Membership{}, invalidArgument("valid auth user id is required")
	}
	return s.activeMembership(ctx, target, tenant,
		errs.B().Code(errs.NotFound).Msg("user is not an active member of this workspace").Err())
}

// membershipScope resolves the authenticated user and tenant and the
// standard-auth service that owns their memberships.
func membershipScope(ctx context.Context) (*Service, authdb.UUID, authdb.UUID, error) {
	data, ok := currentAuthDataFromContext(ctx)
	if !ok {
		return nil, authdb.UUID{}, authdb.UUID{}, unauthenticated("membership requires auth")
	}
	caller, err := parseUUID(string(data.UserID))
	if err != nil {
		return nil, authdb.UUID{}, authdb.UUID{}, unauthenticated("invalid user id")
	}
	tenant, err := parseUUID(string(data.TenantID))
	if err != nil {
		return nil, authdb.UUID{}, authdb.UUID{}, unauthenticated("an organization session is required")
	}
	if !currentStandardConfig().Enabled {
		return nil, authdb.UUID{}, authdb.UUID{}, failedPrecondition("standard auth is not configured")
	}
	svc, err := standardAuthService(ctx)
	if err != nil || svc == nil {
		return nil, authdb.UUID{}, authdb.UUID{}, errs.B().Code(errs.FailedPrecondition).Msg("standard auth is unavailable").Cause(err).Err()
	}
	return svc, caller, tenant, nil
}

// activeMembership reads an enabled user's active membership, or returns
// missing when there is none.
func (s *Service) activeMembership(ctx context.Context, userID, tenant authdb.UUID, missing error) (Membership, error) {
	user, err := s.query.GetUserByID(ctx, userID)
	if isNoRows(err) {
		return Membership{}, missing
	}
	if err != nil {
		return Membership{}, err
	}
	if user.DisabledAt.Valid {
		return Membership{}, missing
	}
	row, err := s.query.GetActiveMembership(ctx, authdb.GetActiveMembershipParams{UserID: userID, TenantID: tenant})
	if isNoRows(err) {
		return Membership{}, missing
	}
	if err != nil {
		return Membership{}, err
	}
	return Membership{
		UserID: AuthUserID(uuidString(row.UserID)), TenantID: TenantID(uuidString(row.TenantID)),
		Role: strings.TrimSpace(row.Role),
	}, nil
}
