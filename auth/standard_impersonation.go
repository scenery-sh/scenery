package auth

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	authdb "github.com/pbrazdil/onlava/auth/db/gen"
)

type StartImpersonationParams struct {
	TargetUserID string `json:"target_user_id"`
	TenantID     string `json:"tenant_id,omitempty"`
	Reason       string `json:"reason"`
}

// StartImpersonation starts a short-lived platform support impersonation session.
//
//onlava:api auth method=POST path=/auth/impersonation/start
func (s *Service) StartImpersonation(ctx context.Context, params *StartImpersonationParams) (*AuthSessionResponse, error) {
	if params == nil || strings.TrimSpace(params.TargetUserID) == "" {
		return nil, invalidArgument("target_user_id is required")
	}
	reason := strings.TrimSpace(params.Reason)
	if reason == "" {
		return nil, invalidArgument("reason is required")
	}
	authData, err := currentAuthData()
	if err != nil {
		return nil, err
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

	tx, q, err := s.beginTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
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
	tenantID, err := s.ensureActiveTenant(ctx, q, target, preferredTenantID)
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
	s.recordEvent(ctx, q, "impersonation_started", target.ID, actor.ID, tenantID, pgtype.UUID{}, map[string]string{"reason": reason})
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return response, nil
}

// StopImpersonation stops an impersonation session and starts a normal actor session.
//
//onlava:api auth method=POST path=/auth/impersonation/stop
func (s *Service) StopImpersonation(ctx context.Context, params *RefreshParams) (*AuthSessionResponse, error) {
	authData, err := currentAuthData()
	if err != nil {
		return nil, err
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
		_ = tx.Rollback(ctx)
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
	tenantID, err := s.ensureActiveTenant(ctx, q, actor, pgtype.UUID{})
	if err != nil {
		return nil, err
	}
	response, err := s.createAuthSessionResponse(ctx, q, actor, tenantID, defaultRefreshSessionTTL, pgtype.UUID{}, pgtype.UUID{}, "")
	if err != nil {
		return nil, err
	}
	s.recordEvent(ctx, q, "impersonation_stopped", actor.ID, pgtype.UUID{}, tenantID, currentSessionID, nil)
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return response, nil
}
