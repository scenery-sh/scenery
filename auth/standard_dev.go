package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	onlava "github.com/pbrazdil/onlava"
	"github.com/pbrazdil/onlava/errs"
)

const maxDevBootstrapClaimLength = 200

type AuthResponse struct {
	Token string `json:"token"`
}

type DevBootstrapParams struct {
	UserID   string `json:"user_id,omitempty"`
	TenantID string `json:"tenant_id,omitempty"`
}

func DevBootstrap(ctx context.Context, params *DevBootstrapParams) (*AuthResponse, error) {
	cfg := standardAuthState.cfg.DevBootstrap
	if !cfg.Enabled {
		return nil, errs.B().Code(errs.NotFound).Msg("endpoint not found").Err()
	}
	meta := onlava.Meta()
	if meta.Environment.Cloud != onlava.CloudLocal {
		return nil, errs.B().Code(errs.PermissionDenied).Msg("dev bootstrap is only allowed in local environments").Err()
	}

	var rawUserID string
	var rawTenantID string
	if params != nil {
		rawUserID = params.UserID
		rawTenantID = params.TenantID
	}

	var userID string
	var tenantID string
	var err error
	if strings.TrimSpace(rawUserID) == "" && strings.TrimSpace(cfg.DefaultUserEmail) != "" {
		userID, tenantID, err = resolveDevBootstrapEmailDefault(ctx, cfg.DefaultUserEmail, firstNonEmpty(rawTenantID, cfg.DefaultTenantID))
		if err != nil {
			return nil, err
		}
	} else {
		userID, err = normalizeDevBootstrapClaim(rawUserID, cfg.DefaultUserID, "user_id")
		if err != nil {
			return nil, errs.B().Code(errs.InvalidArgument).Msg(err.Error()).Err()
		}
		tenantID, err = normalizeDevBootstrapClaim(rawTenantID, cfg.DefaultTenantID, "tenant_id")
		if err != nil {
			return nil, errs.B().Code(errs.InvalidArgument).Msg(err.Error()).Err()
		}
	}
	token, err := GenerateToken(AuthUserID(userID), TenantID(tenantID), cfg.TokenTTL)
	if err != nil {
		return nil, fmt.Errorf("failed to generate dev token: %w", err)
	}
	return &AuthResponse{Token: token}, nil
}

func resolveDevBootstrapEmailDefault(ctx context.Context, email string, preferredTenantID string) (string, string, error) {
	normalizedEmail, err := normalizeEmail(email)
	if err != nil {
		return "", "", errs.B().Code(errs.InvalidArgument).Msg(err.Error()).Err()
	}
	svc, err := standardAuthService(ctx)
	if err != nil {
		return "", "", err
	}
	user, err := svc.query.GetUserByNormalizedEmail(ctx, normalizedEmail)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", errs.B().Code(errs.NotFound).Msg("default user email not found").Err()
		}
		return "", "", err
	}
	if user.DisabledAt.Valid {
		return "", "", permissionDenied("default user is disabled")
	}

	var preferredTenant pgtype.UUID
	if strings.TrimSpace(preferredTenantID) != "" {
		preferredTenant, err = parseUUID(preferredTenantID)
		if err != nil {
			return "", "", errs.B().Code(errs.InvalidArgument).Msg("tenant_id is invalid").Err()
		}
	}
	memberships, err := svc.query.ListUserMemberships(ctx, user.ID)
	if err != nil {
		return "", "", err
	}
	for _, membership := range memberships {
		if preferredTenant.Valid && uuidString(membership.TenantID) == uuidString(preferredTenant) {
			return uuidString(user.ID), uuidString(membership.TenantID), nil
		}
	}
	if preferredTenant.Valid {
		return "", "", permissionDenied("default user is not a member of the configured tenant")
	}
	if len(memberships) == 0 {
		return "", "", failedPrecondition("default user has no active tenant memberships")
	}
	return uuidString(user.ID), uuidString(memberships[0].TenantID), nil
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
