package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	authdb "scenery.sh/auth/db/gen"
	"scenery.sh/errs"
)

// Service owns standard auth, sessions, organizations, invites, and impersonation.
type Service struct {
	db    *sql.DB
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
	// RefreshToken is resolved from the runtime's refresh cookie when empty;
	// see refreshCookieName.
	RefreshToken string
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

func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

func (s *Service) clock() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Service) beginTx(ctx context.Context) (*sql.Tx, *authdb.Queries, error) {
	tx, err := s.db.BeginTx(ctx, nil)
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

func (s *Service) recordEvent(ctx context.Context, q authdb.Querier, eventType string, userID authdb.UUID, actorUserID authdb.UUID, tenantID authdb.UUID, sessionID authdb.UUID, metadata any) {
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
