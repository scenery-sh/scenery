package auth

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// secrets is populated when the standard auth module is registered.
var secrets struct {
	JWTSecret               string
	GoogleOAuthClientID     string
	GoogleOAuthClientSecret string
	PublicAppURL            string
	APIBaseURL              string
	AuthCookieDomain        string
	AuthEmailFrom           string
}

// AuthUserID is the unique identifier for a standard-auth user.
type AuthUserID string

// TenantID is the unique identifier for a tenant.
type TenantID string

// AuthData is the data associated with an authenticated standard-auth user.
type AuthData struct {
	UserID          AuthUserID
	TenantID        TenantID
	SessionID       string
	ActorUserID     AuthUserID
	ImpersonationID string
}

// AuditIdentity is the impersonation-aware identity applications persist with
// audited work. EffectiveUserID is the subject whose access is exercised;
// ActorUserID is the real initiator of the session.
type AuditIdentity struct {
	EffectiveUserID AuthUserID
	ActorUserID     AuthUserID
	TenantID        TenantID
	SessionID       string
	ImpersonationID string
}

// Impersonating reports whether the token represents a platform impersonation session.
func (d *AuthData) Impersonating() bool {
	return d != nil && strings.TrimSpace(string(d.ActorUserID)) != ""
}

// AuditIdentity returns the complete audit identity carried by this auth data.
func (d *AuthData) AuditIdentity() AuditIdentity {
	if d == nil {
		return AuditIdentity{}
	}
	actor := d.UserID
	if d.Impersonating() {
		actor = d.ActorUserID
	}
	return AuditIdentity{
		EffectiveUserID: d.UserID,
		ActorUserID:     actor,
		TenantID:        d.TenantID,
		SessionID:       d.SessionID,
		ImpersonationID: d.ImpersonationID,
	}
}

// AuditTenantID exposes the active tenant for generic audit context readers.
func (d *AuthData) AuditTenantID() string {
	if d == nil {
		return ""
	}
	return strings.TrimSpace(string(d.TenantID))
}

// AuthHandler authenticates a user based on a standard-auth JWT token.
func AuthHandler(ctx context.Context, token string) (UID, *AuthData, error) {
	data, err := ValidateToken(token)
	if err != nil {
		return "", nil, err
	}
	// DevBootstrap uses a deliberately sessionless token and may use a
	// non-database identifier. Standard interactive sessions always carry sid.
	if strings.TrimSpace(data.SessionID) == "" {
		return UID(data.UserID), data, nil
	}
	svc, err := lifecycleService(ctx)
	if err != nil {
		return "", nil, err
	}
	if err := requireEnabledAuthUser(ctx, svc, data.UserID); err != nil {
		return "", nil, err
	}
	if data.Impersonating() {
		if err := requireEnabledAuthUser(ctx, svc, data.ActorUserID); err != nil {
			return "", nil, err
		}
	}
	if err := requireActiveAuthSession(ctx, svc, data); err != nil {
		return "", nil, err
	}
	return UID(data.UserID), data, nil
}

func requireEnabledAuthUser(ctx context.Context, svc *Service, userID AuthUserID) error {
	id, err := lifecycleUserID(userID)
	if err != nil {
		return unauthenticated("invalid user id")
	}
	_, err = currentUser(ctx, svc, id)
	return err
}

// requireActiveAuthSession makes refresh-session revocation invalidate access
// tokens immediately.
func requireActiveAuthSession(ctx context.Context, svc *Service, data *AuthData) error {
	sessionID, err := parseUUID(data.SessionID)
	if err != nil {
		return unauthenticated("auth session is invalid")
	}
	session, err := svc.query.GetRefreshSessionByID(ctx, sessionID)
	if err != nil {
		if isNoRows(err) {
			return unauthenticated("auth session is invalid")
		}
		return err
	}
	if session.RevokedAt.Valid || !session.ExpiresAt.After(svc.clock()) {
		return unauthenticated("auth session is expired")
	}
	if uuidString(session.UserID) != strings.TrimSpace(string(data.UserID)) ||
		uuidString(session.ActiveTenantID) != strings.TrimSpace(string(data.TenantID)) ||
		uuidString(session.ActorUserID) != strings.TrimSpace(string(data.ActorUserID)) ||
		uuidString(session.ImpersonationID) != strings.TrimSpace(data.ImpersonationID) {
		return unauthenticated("auth session does not match token")
	}
	return nil
}

// ValidateToken parses and validates a signed user JWT.
func ValidateToken(token string) (*AuthData, error) {
	claims := &accessTokenClaims{}
	t, err := jwt.ParseWithClaims(
		strings.TrimSpace(token),
		claims,
		func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			if strings.TrimSpace(secrets.JWTSecret) == "" {
				return nil, fmt.Errorf("JWTSecret is not configured")
			}
			return []byte(secrets.JWTSecret), nil
		},
		jwt.WithExpirationRequired(),
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
	)
	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}
	if !t.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	sub := strings.TrimSpace(claims.Subject)
	if sub == "" {
		return nil, fmt.Errorf("token missing subject claim")
	}
	tenantID := strings.TrimSpace(claims.TenantID)
	if tenantID == "" {
		return nil, fmt.Errorf("token missing tenant_id claim")
	}

	return &AuthData{
		UserID:          AuthUserID(sub),
		TenantID:        TenantID(tenantID),
		SessionID:       strings.TrimSpace(claims.SessionID),
		ActorUserID:     AuthUserID(strings.TrimSpace(claims.ActorSubject)),
		ImpersonationID: strings.TrimSpace(claims.ImpersonationID),
	}, nil
}

type accessTokenClaims struct {
	TenantID        string `json:"tenant_id"`
	SessionID       string `json:"sid,omitempty"`
	ActorSubject    string `json:"actor_sub,omitempty"`
	ImpersonationID string `json:"impersonation_id,omitempty"`
	jwt.RegisteredClaims
}

type AccessTokenOptions struct {
	UserID          AuthUserID
	TenantID        TenantID
	SessionID       string
	ActorUserID     AuthUserID
	ImpersonationID string
	ExpiresIn       time.Duration
	Now             time.Time
}

// GenerateAccessToken generates a signed first-party access JWT.
func GenerateAccessToken(options AccessTokenOptions) (string, error) {
	now := options.Now
	if now.IsZero() {
		now = time.Now()
	}
	expiresIn := options.ExpiresIn
	if expiresIn == 0 {
		expiresIn = defaultAccessTokenTTL
	}
	if strings.TrimSpace(secrets.JWTSecret) == "" {
		return "", fmt.Errorf("JWTSecret is not configured")
	}

	userID := strings.TrimSpace(string(options.UserID))
	if userID == "" {
		return "", fmt.Errorf("user id is required")
	}
	tenantID := strings.TrimSpace(string(options.TenantID))
	if tenantID == "" {
		return "", fmt.Errorf("tenant id is required")
	}

	claims := accessTokenClaims{
		TenantID:        tenantID,
		SessionID:       strings.TrimSpace(options.SessionID),
		ActorSubject:    strings.TrimSpace(string(options.ActorUserID)),
		ImpersonationID: strings.TrimSpace(options.ImpersonationID),
		Subject:         userID,
		IssuedAt:        jwt.NewNumericDate(now),
		ExpiresAt:       jwt.NewNumericDate(now.Add(expiresIn)),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secrets.JWTSecret))
}

// GenerateToken generates a signed JWT token for a user and tenant.
// It expires after the given duration.
func GenerateToken(uid AuthUserID, tid TenantID, expiresIn time.Duration) (string, error) {
	return GenerateAccessToken(AccessTokenOptions{
		UserID:    uid,
		TenantID:  tid,
		ExpiresIn: expiresIn,
	})
}
