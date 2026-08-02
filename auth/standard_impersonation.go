package auth

import (
	"context"
	authdb "scenery.sh/auth/db/gen"
	"strings"
)

type StartImpersonationParams struct {
	TargetUserID string `json:"target_user_id"`
	TenantID     string `json:"tenant_id,omitempty"`
	Reason       string `json:"reason"`
}

// StartImpersonation starts a short-lived platform support impersonation session.
func (s *Service) StartImpersonation(ctx context.Context, params *StartImpersonationParams) (*AuthSessionResponse, error) {
	if params == nil || strings.TrimSpace(params.TargetUserID) == "" {
		return nil, invalidArgument("target_user_id is required")
	}
	reason := strings.TrimSpace(params.Reason)
	if reason == "" {
		return nil, invalidArgument("reason is required")
	}
	authData, ok := currentAuthDataFromContext(ctx)
	if !ok || authData == nil {
		return nil, unauthenticated("authentication required")
	}
	if authData.Impersonating() {
		return nil, permissionDenied("nested impersonation is not allowed")
	}
	actorUserID, err := parseUUID(string(authData.UserID))
	if err != nil {
		return nil, unauthenticated("invalid user id")
	}
	targetUserID, err := parseUUID(params.TargetUserID)
	if err != nil {
		return nil, invalidArgument("target_user_id is invalid")
	}
	preferredTenantID, err := nullableUUID(params.TenantID)
	if err != nil {
		return nil, invalidArgument("tenant_id is invalid")
	}
	if uuidString(actorUserID) == uuidString(targetUserID) {
		return nil, invalidArgument("cannot impersonate the current user")
	}

	tx, q, err := s.beginTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	actor, err := q.GetUserByID(ctx, actorUserID)
	if err != nil {
		return nil, err
	}
	if !actor.CanImpersonateUsers || actor.DisabledAt.Valid {
		return nil, permissionDenied("platform impersonation permission is required")
	}
	target, err := q.GetUserByID(ctx, targetUserID)
	if err != nil {
		return nil, err
	}
	if target.DisabledAt.Valid {
		return nil, permissionDenied("target user is disabled")
	}
	tenantID, err := s.ensureImpersonationTenant(ctx, q, target, preferredTenantID)
	if err != nil {
		return nil, err
	}
	impersonationID, err := newUUID()
	if err != nil {
		return nil, err
	}
	response, err := s.createAuthSessionResponse(ctx, q, target, tenantID, defaultImpersonationTTL, actor.ID, impersonationID, reason)
	if err != nil {
		return nil, err
	}
	s.recordEvent(ctx, q, "impersonation_started", target.ID, actor.ID, tenantID, authdb.UUID{}, map[string]string{"reason": reason})
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return response, nil
}

// ensureImpersonationTenant deliberately omits email verification: the
// privileged actor, not the target, authenticates this short-lived session.
// Unlike ordinary sign-in it never creates a personal tenant.
func (s *Service) ensureImpersonationTenant(ctx context.Context, q authdb.Querier, user authdb.ScenerySceneryAuthUser, preferred authdb.UUID) (authdb.UUID, error) {
	if user.DisabledAt.Valid {
		return authdb.UUID{}, permissionDenied("target user is disabled")
	}
	if !preferred.Valid {
		return authdb.UUID{}, invalidArgument("tenant_id is required")
	}
	if _, err := q.GetActiveMembership(ctx, authdb.GetActiveMembershipParams{UserID: user.ID, TenantID: preferred}); err != nil {
		if isNoRows(err) {
			return authdb.UUID{}, permissionDenied("target user is not an active member of the requested tenant")
		}
		return authdb.UUID{}, err
	}
	return preferred, nil
}

// StopImpersonation stops an impersonation session and starts a normal actor session.
func (s *Service) StopImpersonation(ctx context.Context, params *RefreshParams) (*AuthSessionResponse, error) {
	authData, ok := currentAuthDataFromContext(ctx)
	if !ok || authData == nil {
		return nil, unauthenticated("authentication required")
	}
	if !authData.Impersonating() {
		return nil, failedPrecondition("not impersonating")
	}
	actorUserID, err := parseUUID(string(authData.ActorUserID))
	if err != nil {
		return nil, unauthenticated("invalid actor user id")
	}
	currentSessionID, _ := nullableUUID(authData.SessionID)

	tx, q, err := s.beginTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if currentSessionID.Valid {
		_ = q.RevokeRefreshSession(ctx, authdb.RevokeRefreshSessionParams{
			ID:            currentSessionID,
			RevokedReason: "impersonation_stopped",
		})
	}
	actor, err := q.GetUserByID(ctx, actorUserID)
	if err != nil {
		return nil, err
	}
	tenantID, err := s.ensureActiveTenant(ctx, q, actor, authdb.UUID{})
	if err != nil {
		return nil, err
	}
	response, err := s.createAuthSessionResponse(ctx, q, actor, tenantID, defaultRefreshSessionTTL, authdb.UUID{}, authdb.UUID{}, "")
	if err != nil {
		return nil, err
	}
	s.recordEvent(ctx, q, "impersonation_stopped", actor.ID, authdb.UUID{}, tenantID, currentSessionID, nil)
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return response, nil
}
