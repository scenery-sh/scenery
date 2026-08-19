package auth

import (
	"context"
	"fmt"
	"strings"

	scenery "scenery.sh"
	authdb "scenery.sh/auth/db/gen"
	"scenery.sh/errs"
)

const maxDevBootstrapClaimLength = 200
const defaultDevBootstrapTenantName = "Development Workspace"

type DevBootstrapParams struct {
	UserID   string `json:"user_id,omitempty"`
	TenantID string `json:"tenant_id,omitempty"`
}

// DevBootstrap issues a local-development session. When a default user email
// is configured and no explicit user claim is given, it resolves (or creates)
// that user, guarantees a membership on the requested tenant, and starts a
// real refresh session so browsers keep the cookie-based auth flow. Explicit
// claims skip the database and only mint a bearer token.
func DevBootstrap(ctx context.Context, params *DevBootstrapParams) (*AuthSessionResponse, error) {
	cfg := standardAuthState.cfg.DevBootstrap
	if !cfg.Enabled {
		return nil, errs.B().Code(errs.NotFound).Msg("endpoint not found").Err()
	}
	meta := scenery.Meta()
	if meta.Environment.Cloud != scenery.CloudLocal {
		return nil, errs.B().Code(errs.PermissionDenied).Msg("dev bootstrap is only allowed in local environments").Err()
	}

	var rawUserID string
	var rawTenantID string
	if params != nil {
		rawUserID = params.UserID
		rawTenantID = params.TenantID
	}

	if strings.TrimSpace(rawUserID) == "" && strings.TrimSpace(cfg.DefaultUserEmail) != "" {
		return devBootstrapEmailSession(ctx, cfg.DefaultUserEmail, firstNonEmpty(rawTenantID, cfg.DefaultTenantID))
	}

	userID, err := normalizeDevBootstrapClaim(rawUserID, cfg.DefaultUserID, "user_id")
	if err != nil {
		return nil, errs.B().Code(errs.InvalidArgument).Msg(err.Error()).Err()
	}
	tenantID, err := normalizeDevBootstrapClaim(rawTenantID, cfg.DefaultTenantID, "tenant_id")
	if err != nil {
		return nil, errs.B().Code(errs.InvalidArgument).Msg(err.Error()).Err()
	}
	token, err := GenerateToken(AuthUserID(userID), TenantID(tenantID), cfg.TokenTTL)
	if err != nil {
		return nil, fmt.Errorf("failed to generate dev token: %w", err)
	}
	return &AuthSessionResponse{Token: token}, nil
}

func devBootstrapEmailSession(ctx context.Context, email string, preferredTenantID string) (*AuthSessionResponse, error) {
	normalizedEmail, err := normalizeEmail(email)
	if err != nil {
		return nil, errs.B().Code(errs.InvalidArgument).Msg(err.Error()).Err()
	}
	svc, err := standardAuthService(ctx)
	if err != nil {
		return nil, err
	}
	tx, q, err := svc.beginTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	user, tenantID, err := resolveDevBootstrapEmailUser(ctx, q, email, normalizedEmail, preferredTenantID)
	if err != nil {
		return nil, err
	}
	response, err := svc.createAuthSessionResponse(ctx, q, user, tenantID, defaultRefreshSessionTTL, authdb.UUID{}, authdb.UUID{}, "")
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return response, nil
}

func resolveDevBootstrapEmailUser(ctx context.Context, q authdb.Querier, email string, normalizedEmail string, preferredTenantID string) (authdb.SceneryAuthUser, authdb.UUID, error) {
	user, err := q.GetUserByNormalizedEmail(ctx, normalizedEmail)
	if err != nil {
		if isNoRows(err) {
			return createDevBootstrapEmailUser(ctx, q, email, normalizedEmail, preferredTenantID)
		}
		return user, authdb.UUID{}, err
	}
	if user.DisabledAt.Valid {
		return user, authdb.UUID{}, permissionDenied("default user is disabled")
	}

	var preferredTenant authdb.UUID
	if strings.TrimSpace(preferredTenantID) != "" {
		preferredTenant, err = parseUUID(preferredTenantID)
		if err != nil {
			return user, authdb.UUID{}, errs.B().Code(errs.InvalidArgument).Msg("tenant_id is invalid").Err()
		}
	}
	memberships, err := q.ListUserMemberships(ctx, user.ID)
	if err != nil {
		return user, authdb.UUID{}, err
	}
	for _, membership := range memberships {
		if preferredTenant.Valid && uuidString(membership.TenantID) == uuidString(preferredTenant) {
			return user, membership.TenantID, nil
		}
	}
	if preferredTenant.Valid {
		tenantID, err := ensureDevBootstrapTenantMembership(ctx, q, user.ID, preferredTenant)
		if err != nil {
			return user, authdb.UUID{}, err
		}
		return user, tenantID, nil
	}
	if len(memberships) == 0 {
		return user, authdb.UUID{}, failedPrecondition("default user has no active tenant memberships")
	}
	return user, memberships[0].TenantID, nil
}

func createDevBootstrapEmailUser(ctx context.Context, q authdb.Querier, email string, normalizedEmail string, preferredTenantID string) (authdb.SceneryAuthUser, authdb.UUID, error) {
	var user authdb.SceneryAuthUser
	tenantID, err := parseUUID(preferredTenantID)
	if err != nil {
		return user, authdb.UUID{}, errs.B().Code(errs.InvalidArgument).Msg("tenant_id is invalid").Err()
	}
	userID, err := newUUID()
	if err != nil {
		return user, authdb.UUID{}, err
	}
	user, err = q.EnsureDevBootstrapUser(ctx, authdb.EnsureDevBootstrapUserParams{
		ID:                     userID,
		DisplayName:            defaultDisplayName(normalizedEmail, ""),
		PrimaryEmail:           displayEmail(email),
		NormalizedPrimaryEmail: normalizedEmail,
	})
	if err != nil {
		return user, authdb.UUID{}, err
	}
	if user.DisabledAt.Valid {
		return user, authdb.UUID{}, permissionDenied("default user is disabled")
	}
	tenantID, err = ensureDevBootstrapTenantMembership(ctx, q, user.ID, tenantID)
	if err != nil {
		return user, authdb.UUID{}, err
	}
	return user, tenantID, nil
}

func ensureDevBootstrapTenantMembership(ctx context.Context, q authdb.Querier, userID authdb.UUID, tenantID authdb.UUID) (authdb.UUID, error) {
	tenant, err := q.EnsureDevBootstrapTenant(ctx, authdb.EnsureDevBootstrapTenantParams{
		ID:   tenantID,
		Name: defaultDevBootstrapTenantName,
	})
	if err != nil {
		return authdb.UUID{}, err
	}
	if tenant.DeletedAt.Valid {
		return authdb.UUID{}, failedPrecondition("default tenant is deleted")
	}
	membershipID, err := newUUID()
	if err != nil {
		return authdb.UUID{}, err
	}
	if _, err := q.CreateOrganizationMembership(ctx, authdb.CreateOrganizationMembershipParams{
		ID:       membershipID,
		TenantID: tenant.ID,
		UserID:   userID,
		Role:     roleOwner,
	}); err != nil {
		return authdb.UUID{}, err
	}
	return tenant.ID, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func normalizeDevBootstrapClaim(raw string, fallback string, field string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return fallback, nil
	}
	if len(value) > maxDevBootstrapClaimLength {
		return "", fmt.Errorf("%s must be <= %d characters", field, maxDevBootstrapClaimLength)
	}
	if strings.ContainsAny(value, "\r\n\x00") {
		return "", fmt.Errorf("%s contains invalid characters", field)
	}
	return value, nil
}
