package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	authdb "scenery.sh/auth/db/gen"
	"scenery.sh/errs"
)

// Service owns standard auth, sessions, organizations, invites, and impersonation.
type Service struct {
	db    *pgxpool.Pool
	query *authdb.Queries
	now   func() time.Time
}

type UserProfile struct {
	ID                  string `json:"id"`
	Email               string `json:"email"`
	DisplayName         string `json:"display_name"`
	AvatarURL           string `json:"avatar_url"`
	EmailVerified       bool   `json:"email_verified"`
	CanImpersonateUsers bool   `json:"can_impersonate_users"`
}

type OrganizationSession struct {
	TenantID   string `json:"tenant_id"`
	TenantName string `json:"tenant_name"`
	Role       string `json:"role"`
}

type ImpersonationState struct {
	ActorUserID     string `json:"actor_user_id,omitempty"`
	ImpersonationID string `json:"impersonation_id,omitempty"`
	Reason          string `json:"reason,omitempty"`
}

type AuthBootstrapResponse struct {
	Token           string                `json:"token"`
	User            UserProfile           `json:"user"`
	CurrentTenantID string                `json:"current_tenant_id"`
	Organizations   []OrganizationSession `json:"organizations"`
	Impersonation   ImpersonationState    `json:"impersonation"`
}

type AuthSessionResponse struct {
	AuthBootstrapResponse
	SetCookie string `json:"-" header:"Set-Cookie"`
}

type EmailSignupParams struct {
	Email        string `json:"email"`
	Password     string `json:"password"`
	DisplayName  string `json:"display_name,omitempty"`
	RedirectPath string `json:"redirect_path,omitempty"`
}

type EmailSignupResponse struct {
	RequiresEmailVerification bool   `json:"requires_email_verification"`
	Email                     string `json:"email"`
	DevVerificationToken      string `json:"dev_verification_token,omitempty"`
}

type EmailLoginParams struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type RefreshParams struct {
	RefreshToken string `cookie:"onlv_refresh"`
}

type PasswordResetRequestParams struct {
	Email string `json:"email"`
}

type PasswordResetRequestResponse struct {
	OK            bool   `json:"ok"`
	DevResetToken string `json:"dev_reset_token,omitempty"`
}

type PasswordResetConfirmParams struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

func invalidArgument(message string) error {
	return errs.B().Code(errs.InvalidArgument).Msg(message).Err()
}

func unauthenticated(message string) error {
	if strings.TrimSpace(message) == "" {
		message = "unauthorized"
	}
	return errs.B().Code(errs.Unauthenticated).Msg(message).Err()
}

func permissionDenied(message string) error {
	if strings.TrimSpace(message) == "" {
		message = "forbidden"
	}
	return errs.B().Code(errs.PermissionDenied).Msg(message).Err()
}

func failedPrecondition(message string) error {
	return errs.B().Code(errs.FailedPrecondition).Msg(message).Err()
}

func alreadyExists(message string) error {
	return errs.B().Code(errs.AlreadyExists).Msg(message).Err()
}

func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

func isUniqueViolation(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == "23505"
}

func (s *Service) clock() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Service) beginTx(ctx context.Context) (pgx.Tx, *authdb.Queries, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	return tx, s.query.WithTx(tx), nil
}

func mapUser(row authdb.SceneryAuthUser) UserProfile {
	return UserProfile{
		ID:                  uuidString(row.ID),
		Email:               strings.TrimSpace(row.PrimaryEmail),
		DisplayName:         strings.TrimSpace(row.DisplayName),
		AvatarURL:           strings.TrimSpace(row.AvatarUrl),
		EmailVerified:       row.EmailVerifiedAt.Valid,
		CanImpersonateUsers: row.CanImpersonateUsers,
	}
}

func mapOrganization(row authdb.ListUserMembershipsRow) OrganizationSession {
	return OrganizationSession{
		TenantID:   uuidString(row.TenantID),
		TenantName: strings.TrimSpace(row.TenantName),
		Role:       strings.TrimSpace(row.Role),
	}
}

func (s *Service) buildBootstrap(ctx context.Context, q authdb.Querier, user authdb.SceneryAuthUser, tenantID pgtype.UUID, session authdb.SceneryAuthRefreshSession) (*AuthBootstrapResponse, error) {
	memberships, err := q.ListUserMemberships(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	organizations := make([]OrganizationSession, 0, len(memberships))
	tenantAllowed := false
	for _, membership := range memberships {
		if uuidString(membership.TenantID) == uuidString(tenantID) {
			tenantAllowed = true
		}
		organizations = append(organizations, mapOrganization(membership))
	}
	if !tenantAllowed {
		return nil, permissionDenied("workspace access is disabled")
	}

	impersonation := ImpersonationState{}
	if session.ActorUserID.Valid {
		impersonation.ActorUserID = uuidString(session.ActorUserID)
		impersonation.ImpersonationID = uuidString(session.ImpersonationID)
		impersonation.Reason = strings.TrimSpace(session.ImpersonationReason)
	}

	token, err := GenerateAccessToken(AccessTokenOptions{
		UserID:          AuthUserID(uuidString(user.ID)),
		TenantID:        TenantID(uuidString(tenantID)),
		SessionID:       uuidString(session.ID),
		ActorUserID:     AuthUserID(uuidString(session.ActorUserID)),
		ImpersonationID: uuidString(session.ImpersonationID),
		ExpiresIn:       defaultAccessTokenTTL,
		Now:             s.clock(),
	})
	if err != nil {
		return nil, err
	}

	return &AuthBootstrapResponse{
		Token:           token,
		User:            mapUser(user),
		CurrentTenantID: uuidString(tenantID),
		Organizations:   organizations,
		Impersonation:   impersonation,
	}, nil
}

func (s *Service) ensureActiveTenant(ctx context.Context, q authdb.Querier, user authdb.SceneryAuthUser, preferred pgtype.UUID) (pgtype.UUID, error) {
	if !user.EmailVerifiedAt.Valid {
		return pgtype.UUID{}, failedPrecondition("email verification is required")
	}
	if user.DisabledAt.Valid {
		return pgtype.UUID{}, permissionDenied("user is disabled")
	}

	memberships, err := q.ListUserMemberships(ctx, user.ID)
	if err != nil {
		return pgtype.UUID{}, err
	}
	for _, membership := range memberships {
		if preferred.Valid && uuidString(membership.TenantID) == uuidString(preferred) {
			return membership.TenantID, nil
		}
	}
	if len(memberships) > 0 {
		return memberships[0].TenantID, nil
	}

	tenantID, err := newUUID()
	if err != nil {
		return pgtype.UUID{}, err
	}
	tenant, err := q.CreateTenant(ctx, authdb.CreateTenantParams{
		ID:   tenantID,
		Name: defaultWorkspaceName(user.DisplayName),
	})
	if err != nil {
		return pgtype.UUID{}, err
	}
	membershipID, err := newUUID()
	if err != nil {
		return pgtype.UUID{}, err
	}
	if _, err := q.CreateOrganizationMembership(ctx, authdb.CreateOrganizationMembershipParams{
		ID:       membershipID,
		TenantID: tenant.ID,
		UserID:   user.ID,
		Role:     roleOwner,
	}); err != nil {
		return pgtype.UUID{}, err
	}
	return tenant.ID, nil
}

func (s *Service) createRefreshSession(ctx context.Context, q authdb.Querier, userID pgtype.UUID, tenantID pgtype.UUID, ttl time.Duration, actorUserID pgtype.UUID, impersonationID pgtype.UUID, reason string) (authdb.SceneryAuthRefreshSession, string, error) {
	sessionID, err := newUUID()
	if err != nil {
		return authdb.SceneryAuthRefreshSession{}, "", err
	}
	rawToken, err := newRefreshToken(sessionID)
	if err != nil {
		return authdb.SceneryAuthRefreshSession{}, "", err
	}
	session, err := q.CreateRefreshSession(ctx, authdb.CreateRefreshSessionParams{
		ID:                  sessionID,
		UserID:              userID,
		TokenHash:           tokenHash(rawToken),
		ActiveTenantID:      tenantID,
		ExpiresAt:           timestamptz(s.clock().Add(ttl)),
		UserAgent:           requestUserAgent(),
		IpHash:              requestIPHash(),
		ActorUserID:         actorUserID,
		ImpersonationID:     impersonationID,
		ImpersonationReason: strings.TrimSpace(reason),
	})
	if err != nil {
		return authdb.SceneryAuthRefreshSession{}, "", err
	}
	return session, rawToken, nil
}

func (s *Service) createAuthSessionResponse(ctx context.Context, q authdb.Querier, user authdb.SceneryAuthUser, tenantID pgtype.UUID, ttl time.Duration, actorUserID pgtype.UUID, impersonationID pgtype.UUID, reason string) (*AuthSessionResponse, error) {
	session, rawToken, err := s.createRefreshSession(ctx, q, user.ID, tenantID, ttl, actorUserID, impersonationID, reason)
	if err != nil {
		return nil, err
	}
	bootstrap, err := s.buildBootstrap(ctx, q, user, tenantID, session)
	if err != nil {
		return nil, err
	}
	return &AuthSessionResponse{
		AuthBootstrapResponse: *bootstrap,
		SetCookie:             refreshCookie(rawToken, session.ExpiresAt.Time),
	}, nil
}

func refreshCookie(value string, expiresAt time.Time) string {
	cookie := (&http.Cookie{
		Name:     refreshCookieName,
		Value:    strings.TrimSpace(value),
		Path:     "/auth",
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
		HttpOnly: true,
		Secure:   refreshCookieSecure(isLocalRuntime(), requestHeaders(), secrets.APIBaseURL),
		SameSite: http.SameSiteLaxMode,
	}).String()
	if domain := strings.TrimSpace(secrets.AuthCookieDomain); domain != "" {
		cookie += "; Domain=" + domain
	}
	return cookie
}

func clearRefreshCookie() string {
	cookie := (&http.Cookie{
		Name:     refreshCookieName,
		Value:    "",
		Path:     "/auth",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   refreshCookieSecure(isLocalRuntime(), requestHeaders(), secrets.APIBaseURL),
		SameSite: http.SameSiteLaxMode,
	}).String()
	if domain := strings.TrimSpace(secrets.AuthCookieDomain); domain != "" {
		cookie += "; Domain=" + domain
	}
	return cookie
}

func refreshCookieSecure(localRuntime bool, headers http.Header, apiBaseURL string) bool {
	if localRuntime && (isForwardedHTTPS(headers) || isHTTPSOrigin(headers) || isHTTPSURL(apiBaseURL)) {
		return true
	}
	return !localRuntime
}

func isForwardedHTTPS(headers http.Header) bool {
	return strings.EqualFold(strings.TrimSpace(headers.Get("X-Forwarded-Proto")), "https")
}

func isHTTPSOrigin(headers http.Header) bool {
	return isHTTPSURL(headers.Get("Origin"))
}

func isHTTPSURL(value string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "https://")
}

func (s *Service) recordEvent(ctx context.Context, q authdb.Querier, eventType string, userID pgtype.UUID, actorUserID pgtype.UUID, tenantID pgtype.UUID, sessionID pgtype.UUID, metadata any) {
	id, err := newUUID()
	if err != nil {
		return
	}
	_ = q.CreateAuthEvent(ctx, authdb.CreateAuthEventParams{
		ID:          id,
		EventType:   eventType,
		UserID:      userID,
		ActorUserID: actorUserID,
		TenantID:    tenantID,
		SessionID:   sessionID,
		IpHash:      requestIPHash(),
		UserAgent:   requestUserAgent(),
		Metadata:    jsonBytes(metadata),
	})
}

func (s *Service) checkRateLimit(ctx context.Context, q authdb.Querier, purpose string, normalizedEmail string) error {
	id, err := newUUID()
	if err != nil {
		return err
	}
	attempt, err := q.UpsertAuthAttempt(ctx, authdb.UpsertAuthAttemptParams{
		ID:              id,
		Purpose:         purpose,
		NormalizedEmail: normalizedEmail,
		IpHash:          requestIPHash(),
	})
	if err != nil {
		return err
	}
	if attempt.AttemptCount > 20 {
		return errs.B().Code(errs.ResourceExhausted).Msg("too many auth attempts; try again later").Err()
	}
	return nil
}
