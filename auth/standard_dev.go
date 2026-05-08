package auth

import (
	"context"
	"fmt"
	"strings"

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

func DevBootstrap(_ context.Context, params *DevBootstrapParams) (*AuthResponse, error) {
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
	return &AuthResponse{Token: token}, nil
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
