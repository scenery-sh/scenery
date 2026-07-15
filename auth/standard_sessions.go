package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	authdb "scenery.sh/auth/db/gen"
)

// errRefreshReplay signals that a replayed refresh token was detected and the
// session family was revoked on the caller's transaction. The caller must
// commit that revocation before rejecting the request, otherwise the revoke is
// rolled back and the stolen session stays alive.
var errRefreshReplay = errors.New("refresh session replay detected")

type EmailVerificationConfirmParams struct {
	Token string `json:"token"`
}

type EmailVerificationResendParams struct {
	Email string `json:"email"`
}

type EmailVerificationResendResponse struct {
	OK                   bool   `json:"ok"`
	DevVerificationToken string `json:"dev_verification_token,omitempty"`
}

type LogoutResponse struct {
	OK        bool   `json:"ok"`
	SetCookie string `json:"-" header:"Set-Cookie"`
}

// SignupEmail creates a first-party email/password user and sends an email verification token.
func (s *Service) SignupEmail(ctx context.Context, params *EmailSignupParams) (*EmailSignupResponse, error) {
	if params == nil {
		return nil, invalidArgument("request body is required")
	}
	normalizedEmail, err := normalizeEmail(params.Email)
	if err != nil {
		return nil, invalidArgument(err.Error())
	}
	if err := validatePassword(params.Password); err != nil {
		return nil, invalidArgument(err.Error())
	}
	passwordHash, err := hashPassword(params.Password)
	if err != nil {
		return nil, err
	}

	tx, q, err := s.beginTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := s.checkRateLimit(ctx, q, "signup_email", normalizedEmail); err != nil {
		return nil, err
	}
	if _, err := q.GetUserByNormalizedEmail(ctx, normalizedEmail); err == nil {
		return nil, alreadyExists("email is already registered")
	} else if !isNoRows(err) {
		return nil, err
	}

	userID, err := newUUID()
	if err != nil {
		return nil, err
	}
	user, err := q.CreateUser(ctx, authdb.CreateUserParams{
		ID:                     userID,
		DisplayName:            defaultDisplayName(normalizedEmail, params.DisplayName),
		PrimaryEmail:           displayEmail(params.Email),
		NormalizedPrimaryEmail: normalizedEmail,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return nil, alreadyExists("email is already registered")
		}
		return nil, err
	}

	identityID, err := newUUID()
	if err != nil {
		return nil, err
	}
	if _, err := q.CreateAuthIdentity(ctx, authdb.CreateAuthIdentityParams{
		ID:              identityID,
		UserID:          user.ID,
		Provider:        identityProviderEmail,
		ProviderSubject: normalizedEmail,
		Email:           displayEmail(params.Email),
		NormalizedEmail: normalizedEmail,
		PasswordHash:    passwordHash,
	}); err != nil {
		if isUniqueViolation(err) {
			return nil, alreadyExists("email is already registered")
		}
		return nil, err
	}

	rawToken, err := s.createOneTimeToken(ctx, q, tokenPurposeEmailVerification, user.ID, authdb.UUID{}, displayEmail(params.Email), normalizedEmail, map[string]string{
		"redirect_path": safeRedirectPath(params.RedirectPath),
	}, defaultEmailVerificationTTL)
	if err != nil {
		return nil, err
	}
	s.recordEvent(ctx, q, "signup_email", user.ID, authdb.UUID{}, authdb.UUID{}, authdb.UUID{}, nil)

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	_ = sendVerificationEmail(ctx, displayEmail(params.Email), rawToken, safeRedirectPath(params.RedirectPath))
	response := &EmailSignupResponse{
		RequiresEmailVerification: true,
		Email:                     displayEmail(params.Email),
	}
	if isLocalRuntime() {
		response.DevVerificationToken = rawToken
	}
	return response, nil
}

// ConfirmEmailVerification consumes an email verification token and starts a session.
func (s *Service) ConfirmEmailVerification(ctx context.Context, params *EmailVerificationConfirmParams) (*AuthSessionResponse, error) {
	if params == nil || strings.TrimSpace(params.Token) == "" {
		return nil, invalidArgument("token is required")
	}

	tx, q, err := s.beginTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	oneTime, err := q.ConsumeOneTimeToken(ctx, authdb.ConsumeOneTimeTokenParams{
		TokenHash: tokenHash(params.Token),
		Purpose:   tokenPurposeEmailVerification,
	})
	if err != nil {
		if isNoRows(err) {
			return nil, invalidArgument("verification token is invalid or expired")
		}
		return nil, err
	}
	if !oneTime.UserID.Valid {
		return nil, invalidArgument("verification token is invalid")
	}

	user, err := q.MarkUserEmailVerified(ctx, oneTime.UserID)
	if err != nil {
		return nil, err
	}
	tenantID, err := s.ensureActiveTenant(ctx, q, user, authdb.UUID{})
	if err != nil {
		return nil, err
	}
	response, err := s.createAuthSessionResponse(ctx, q, user, tenantID, defaultRefreshSessionTTL, authdb.UUID{}, authdb.UUID{}, "")
	if err != nil {
		return nil, err
	}
	s.recordEvent(ctx, q, "email_verified", user.ID, authdb.UUID{}, tenantID, authdb.UUID{}, nil)
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return response, nil
}

// ResendEmailVerification creates a new email verification token for an unverified user.
func (s *Service) ResendEmailVerification(ctx context.Context, params *EmailVerificationResendParams) (*EmailVerificationResendResponse, error) {
	if params == nil {
		return nil, invalidArgument("request body is required")
	}
	normalizedEmail, err := normalizeEmail(params.Email)
	if err != nil {
		return nil, invalidArgument(err.Error())
	}

	tx, q, err := s.beginTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if err := s.checkRateLimit(ctx, q, "email_verification_resend", normalizedEmail); err != nil {
		return nil, err
	}

	user, err := q.GetUserByNormalizedEmail(ctx, normalizedEmail)
	if err != nil {
		if isNoRows(err) {
			return &EmailVerificationResendResponse{OK: true}, tx.Commit()
		}
		return nil, err
	}
	if user.EmailVerifiedAt.Valid {
		return &EmailVerificationResendResponse{OK: true}, tx.Commit()
	}
	rawToken, err := s.createOneTimeToken(ctx, q, tokenPurposeEmailVerification, user.ID, authdb.UUID{}, displayEmail(params.Email), normalizedEmail, nil, defaultEmailVerificationTTL)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	_ = sendVerificationEmail(ctx, displayEmail(params.Email), rawToken, "")
	response := &EmailVerificationResendResponse{OK: true}
	if isLocalRuntime() {
		response.DevVerificationToken = rawToken
	}
	return response, nil
}

// LoginEmail verifies an email/password identity and starts a refresh session.
func (s *Service) LoginEmail(ctx context.Context, params *EmailLoginParams) (*AuthSessionResponse, error) {
	if params == nil {
		return nil, invalidArgument("request body is required")
	}
	normalizedEmail, err := normalizeEmail(params.Email)
	if err != nil {
		return nil, invalidArgument(err.Error())
	}

	tx, q, err := s.beginTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if err := s.checkRateLimit(ctx, q, "login_email", normalizedEmail); err != nil {
		return nil, err
	}

	identity, err := q.GetEmailIdentityForLogin(ctx, normalizedEmail)
	if err != nil {
		if isNoRows(err) {
			return nil, unauthenticated("email or password is invalid")
		}
		return nil, err
	}
	ok, needsUpgrade, err := verifyPassword(params.Password, identity.PasswordHash)
	if err != nil || !ok {
		return nil, unauthenticated("email or password is invalid")
	}
	if needsUpgrade {
		nextHash, hashErr := hashPassword(params.Password)
		if hashErr == nil {
			_, _ = q.UpdateIdentityPasswordHash(ctx, authdb.UpdateIdentityPasswordHashParams{
				ID:           identity.ID,
				PasswordHash: nextHash,
			})
		}
	}

	user, err := q.GetUserByID(ctx, identity.UserID)
	if err != nil {
		return nil, err
	}
	if !user.EmailVerifiedAt.Valid {
		return nil, failedPrecondition("email verification is required")
	}
	tenantID, err := s.ensureActiveTenant(ctx, q, user, authdb.UUID{})
	if err != nil {
		return nil, err
	}
	response, err := s.createAuthSessionResponse(ctx, q, user, tenantID, defaultRefreshSessionTTL, authdb.UUID{}, authdb.UUID{}, "")
	if err != nil {
		return nil, err
	}
	s.recordEvent(ctx, q, "login_email", user.ID, authdb.UUID{}, tenantID, authdb.UUID{}, nil)
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return response, nil
}

// Refresh rotates the refresh cookie and returns a fresh access token.
func (s *Service) Refresh(ctx context.Context, params *RefreshParams) (*AuthSessionResponse, error) {
	rawRefreshToken := refreshTokenFromParams(params)
	if rawRefreshToken == "" {
		return nil, unauthenticated("refresh session is missing")
	}

	tx, q, err := s.beginTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	session, rawToken, err := s.rotateRefreshSession(ctx, q, rawRefreshToken)
	if err != nil {
		if errors.Is(err, errRefreshReplay) {
			// Persist the replay revocation instead of discarding it with the
			// deferred rollback; the request is still rejected afterward.
			if commitErr := tx.Commit(); commitErr != nil {
				return nil, commitErr
			}
			return nil, unauthenticated("refresh session is invalid")
		}
		return nil, err
	}
	user, err := q.GetUserByID(ctx, session.UserID)
	if err != nil {
		return nil, err
	}
	tenantID, err := s.ensureActiveTenant(ctx, q, user, session.ActiveTenantID)
	if err != nil {
		return nil, err
	}
	if uuidString(tenantID) != uuidString(session.ActiveTenantID) {
		session, err = q.SetRefreshSessionTenant(ctx, authdb.SetRefreshSessionTenantParams{
			ID:             session.ID,
			ActiveTenantID: tenantID,
		})
		if err != nil {
			return nil, err
		}
	}
	bootstrap, err := s.buildBootstrap(ctx, q, user, tenantID, session)
	if err != nil {
		return nil, err
	}
	s.recordEvent(ctx, q, "refresh", user.ID, session.ActorUserID, tenantID, session.ID, nil)
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &AuthSessionResponse{
		AuthBootstrapResponse: *bootstrap,
		SetCookie:             refreshCookie(rawToken, session.ExpiresAt),
	}, nil
}

// Logout revokes the current refresh session and clears the refresh cookie.
func (s *Service) Logout(ctx context.Context, params *RefreshParams) (*LogoutResponse, error) {
	rawRefreshToken := refreshTokenFromParams(params)
	if rawRefreshToken != "" {
		if sessionID, err := parseRefreshToken(rawRefreshToken); err == nil {
			_ = s.query.RevokeRefreshSession(ctx, authdb.RevokeRefreshSessionParams{
				ID:            sessionID,
				RevokedReason: "logout",
			})
		}
	}
	return &LogoutResponse{
		OK:        true,
		SetCookie: clearRefreshCookie(),
	}, nil
}

func refreshTokenFromParams(params *RefreshParams) string {
	return resolveRefreshToken(params, requestHeaders())
}

// Me returns the current auth bootstrap state for an access token.
func (s *Service) Me(ctx context.Context) (*AuthBootstrapResponse, error) {
	authData, err := currentAuthData()
	if err != nil {
		return nil, err
	}
	userID, err := parseUUID(string(authData.UserID))
	if err != nil {
		return nil, unauthenticated("invalid user id")
	}
	tenantID, err := parseUUID(string(authData.TenantID))
	if err != nil {
		return nil, unauthenticated("invalid tenant id")
	}
	user, err := s.query.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	session := authdb.SceneryAuthRefreshSession{}
	if authData.SessionID != "" {
		sessionID, parseErr := parseUUID(authData.SessionID)
		if parseErr == nil {
			if row, loadErr := s.query.GetRefreshSessionByID(ctx, sessionID); loadErr == nil {
				session = row
			}
		}
	}
	session.ID, _ = nullableUUID(authData.SessionID)
	session.ActorUserID, _ = nullableUUID(string(authData.ActorUserID))
	session.ImpersonationID, _ = nullableUUID(authData.ImpersonationID)
	return s.buildBootstrap(ctx, s.query, user, tenantID, session)
}

// RequestPasswordReset creates a one-time password reset token when the email exists.
func (s *Service) RequestPasswordReset(ctx context.Context, params *PasswordResetRequestParams) (*PasswordResetRequestResponse, error) {
	if params == nil {
		return nil, invalidArgument("request body is required")
	}
	normalizedEmail, err := normalizeEmail(params.Email)
	if err != nil {
		return nil, invalidArgument(err.Error())
	}
	tx, q, err := s.beginTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if err := s.checkRateLimit(ctx, q, "password_reset_request", normalizedEmail); err != nil {
		return nil, err
	}
	user, err := q.GetUserByNormalizedEmail(ctx, normalizedEmail)
	if err != nil {
		if isNoRows(err) {
			return &PasswordResetRequestResponse{OK: true}, tx.Commit()
		}
		return nil, err
	}
	rawToken, err := s.createOneTimeToken(ctx, q, tokenPurposePasswordReset, user.ID, authdb.UUID{}, displayEmail(params.Email), normalizedEmail, nil, defaultPasswordResetTTL)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	_ = sendPasswordResetEmail(ctx, displayEmail(params.Email), rawToken)
	response := &PasswordResetRequestResponse{OK: true}
	if isLocalRuntime() {
		response.DevResetToken = rawToken
	}
	return response, nil
}

// ConfirmPasswordReset consumes a password reset token, updates the password, revokes old sessions, and starts a fresh session.
func (s *Service) ConfirmPasswordReset(ctx context.Context, params *PasswordResetConfirmParams) (*AuthSessionResponse, error) {
	if params == nil || strings.TrimSpace(params.Token) == "" {
		return nil, invalidArgument("token is required")
	}
	passwordHash, err := hashPassword(params.NewPassword)
	if err != nil {
		return nil, invalidArgument(err.Error())
	}

	tx, q, err := s.beginTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	oneTime, err := q.ConsumeOneTimeToken(ctx, authdb.ConsumeOneTimeTokenParams{
		TokenHash: tokenHash(params.Token),
		Purpose:   tokenPurposePasswordReset,
	})
	if err != nil {
		if isNoRows(err) {
			return nil, invalidArgument("password reset token is invalid or expired")
		}
		return nil, err
	}
	if !oneTime.UserID.Valid {
		return nil, invalidArgument("password reset token is invalid")
	}
	identity, err := q.GetEmailIdentityForLogin(ctx, oneTime.NormalizedEmail)
	if err != nil {
		return nil, err
	}
	if _, err := q.UpdateIdentityPasswordHash(ctx, authdb.UpdateIdentityPasswordHashParams{
		ID:           identity.ID,
		PasswordHash: passwordHash,
	}); err != nil {
		return nil, err
	}
	user, err := q.MarkUserEmailVerified(ctx, oneTime.UserID)
	if err != nil {
		return nil, err
	}
	if err := q.RevokeUserRefreshSessions(ctx, authdb.RevokeUserRefreshSessionsParams{
		UserID:        user.ID,
		RevokedReason: "password_reset",
	}); err != nil {
		return nil, err
	}
	tenantID, err := s.ensureActiveTenant(ctx, q, user, authdb.UUID{})
	if err != nil {
		return nil, err
	}
	response, err := s.createAuthSessionResponse(ctx, q, user, tenantID, defaultRefreshSessionTTL, authdb.UUID{}, authdb.UUID{}, "")
	if err != nil {
		return nil, err
	}
	s.recordEvent(ctx, q, "password_reset", user.ID, authdb.UUID{}, tenantID, authdb.UUID{}, nil)
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return response, nil
}

func (s *Service) createOneTimeToken(ctx context.Context, q authdb.Querier, purpose string, userID authdb.UUID, tenantID authdb.UUID, email string, normalizedEmail string, metadata any, ttl time.Duration) (string, error) {
	id, err := newUUID()
	if err != nil {
		return "", err
	}
	rawToken, err := newRandomToken(32)
	if err != nil {
		return "", err
	}
	if _, err := q.CreateOneTimeToken(ctx, authdb.CreateOneTimeTokenParams{
		ID:              id,
		Purpose:         purpose,
		TokenHash:       tokenHash(rawToken),
		UserID:          userID,
		TenantID:        tenantID,
		Email:           strings.TrimSpace(email),
		NormalizedEmail: strings.TrimSpace(normalizedEmail),
		Metadata:        jsonBytes(metadata),
		ExpiresAt:       s.clock().Add(ttl),
	}); err != nil {
		return "", err
	}
	return rawToken, nil
}

func (s *Service) rotateRefreshSession(ctx context.Context, q authdb.Querier, rawRefreshToken string) (authdb.SceneryAuthRefreshSession, string, error) {
	sessionID, err := parseRefreshToken(rawRefreshToken)
	if err != nil {
		return authdb.SceneryAuthRefreshSession{}, "", unauthenticated("refresh session is invalid")
	}
	session, err := q.GetRefreshSessionByID(ctx, sessionID)
	if err != nil {
		if isNoRows(err) {
			return authdb.SceneryAuthRefreshSession{}, "", unauthenticated("refresh session is invalid")
		}
		return authdb.SceneryAuthRefreshSession{}, "", err
	}
	now := s.clock()
	if session.RevokedAt.Valid || !session.ExpiresAt.After(now) {
		return authdb.SceneryAuthRefreshSession{}, "", unauthenticated("refresh session is expired")
	}

	hash := tokenHash(rawRefreshToken)
	matchesCurrent := hash == session.TokenHash
	matchesPrevious := session.PreviousTokenHash != "" &&
		hash == session.PreviousTokenHash &&
		session.PreviousTokenExpiresAt.Valid &&
		session.PreviousTokenExpiresAt.Time.After(now)
	if !matchesCurrent && !matchesPrevious {
		if err := q.RevokeRefreshSession(ctx, authdb.RevokeRefreshSessionParams{
			ID:            session.ID,
			RevokedReason: "refresh_replay",
		}); err != nil {
			return authdb.SceneryAuthRefreshSession{}, "", err
		}
		return authdb.SceneryAuthRefreshSession{}, "", errRefreshReplay
	}

	nextRawToken, err := newRefreshToken(session.ID)
	if err != nil {
		return authdb.SceneryAuthRefreshSession{}, "", err
	}
	rotated, err := q.RotateRefreshSession(ctx, authdb.RotateRefreshSessionParams{
		ID:        session.ID,
		TokenHash: tokenHash(nextRawToken),
		GraceMs:   int64(refreshTokenReplayGrace / time.Millisecond),
	})
	if err != nil {
		return authdb.SceneryAuthRefreshSession{}, "", err
	}
	return rotated, nextRawToken, nil
}
