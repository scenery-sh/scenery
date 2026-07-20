package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	authdb "scenery.sh/auth/db/gen"
	"scenery.sh/errs"
)

const googleConnectionOAuthPurpose = "google_connection"

type GoogleConnectStartParams struct {
	Scopes       []string `json:"scopes"`
	RedirectPath string   `json:"redirect_path,omitempty"`
}

type GoogleConnectStartResponse struct {
	AuthorizeURL string `json:"authorize_url"`
}

type GoogleConnectionResponse struct {
	Status        string     `json:"status"`
	Email         string     `json:"email,omitempty"`
	Scopes        []string   `json:"scopes,omitempty"`
	ConnectedAt   *time.Time `json:"connected_at,omitempty"`
	LastRefreshAt *time.Time `json:"last_refresh_at,omitempty"`
	ReauthReason  string     `json:"reauth_reason,omitempty"`
}

func (s *Service) GoogleConnectStart(ctx context.Context, params *GoogleConnectStartParams) (*GoogleConnectStartResponse, error) {
	if params == nil {
		return nil, invalidArgument("request body is required")
	}
	if strings.TrimSpace(secrets.GoogleOAuthClientID) == "" || strings.TrimSpace(secrets.GoogleOAuthClientSecret) == "" {
		return nil, failedPrecondition("Google OAuth is not configured")
	}
	userID, err := currentAuthUserID()
	if err != nil {
		return nil, err
	}
	requested, err := validateGoogleScopes(params.Scopes)
	if err != nil {
		return nil, err
	}
	if err := validateGoogleAllowedScopes(requested); err != nil {
		return nil, err
	}

	_ = s.query.DeleteExpiredOAuthStates(ctx)
	state, verifier, nonce, err := newGoogleOAuthMaterial()
	if err != nil {
		return nil, err
	}
	stateID, err := newUUID()
	if err != nil {
		return nil, err
	}
	if _, err := s.query.CreateGoogleConnectionOAuthState(ctx, authdb.CreateGoogleConnectionOAuthStateParams{
		ID:           stateID,
		StateHash:    tokenHash(state),
		PkceVerifier: verifier,
		NonceHash:    tokenHash(nonce),
		UserID:       userID,
		RedirectPath: safeRedirectPath(params.RedirectPath),
		ExpiresAt:    s.clock().Add(defaultOAuthStateTTL),
	}); err != nil {
		return nil, err
	}

	needsConsent := true
	if conn, err := s.query.GetGoogleConnectionByUser(ctx, userID); err == nil {
		needsConsent = !googleConnectionHasUsableRefresh(conn, requested)
	} else if !isNoRows(err) {
		return nil, err
	}

	authURL, _ := url.Parse(googleAuthEndpoint)
	query := authURL.Query()
	query.Set("client_id", strings.TrimSpace(secrets.GoogleOAuthClientID))
	query.Set("redirect_uri", googleConnectionRedirectURI(currentHTTPRequest()))
	query.Set("response_type", "code")
	query.Set("scope", strings.Join(withGoogleIdentityScopes(requested), " "))
	query.Set("state", state)
	query.Set("nonce", nonce)
	query.Set("code_challenge", googlePKCEChallenge(verifier))
	query.Set("code_challenge_method", "S256")
	query.Set("access_type", "offline")
	query.Set("include_granted_scopes", "true")
	if needsConsent {
		query.Set("prompt", "consent")
	}
	authURL.RawQuery = query.Encode()
	return &GoogleConnectStartResponse{AuthorizeURL: authURL.String()}, nil
}

func finishGoogleConnectionCallback(w http.ResponseWriter, req *http.Request, svc *Service, oauthState authdb.ScenerySceneryAuthOauthState, code string, redirectURI string) {
	redirectPath := safeRedirectPath(oauthState.RedirectPath)
	if strings.TrimSpace(oauthState.Purpose) != googleConnectionOAuthPurpose || !oauthState.UserID.Valid {
		redirectGoogleConnectionCallbackError(w, req, redirectPath, "oauth_state")
		return
	}
	tokenResponse, err := exchangeGoogleCode(req.Context(), code, oauthState.PkceVerifier, redirectURI)
	if err != nil {
		redirectGoogleConnectionCallbackError(w, req, redirectPath, "google_token")
		return
	}
	claims, err := verifyGoogleIDToken(req.Context(), tokenResponse.IDToken)
	if err != nil {
		redirectGoogleConnectionCallbackError(w, req, redirectPath, "google_id_token")
		return
	}
	if !claims.EmailVerified {
		redirectGoogleConnectionCallbackError(w, req, redirectPath, "google_email_unverified")
		return
	}
	if oauthState.NonceHash != "" && tokenHash(claims.Nonce) != oauthState.NonceHash {
		redirectGoogleConnectionCallbackError(w, req, redirectPath, "google_id_token")
		return
	}
	if err := svc.finishGoogleConnection(req.Context(), oauthState.UserID, claims, tokenResponse); err != nil {
		redirectGoogleConnectionCallbackError(w, req, redirectPath, googleConnectionCallbackErrorCode(err))
		return
	}
	http.Redirect(w, req, appRedirectURL(req, redirectPathWithQuery(redirectPath, "google_connected", "1")), http.StatusFound)
}

func (s *Service) GetGoogleConnection(ctx context.Context) (*GoogleConnectionResponse, error) {
	userID, err := currentAuthUserID()
	if err != nil {
		return nil, err
	}
	conn, err := s.query.GetGoogleConnectionByUser(ctx, userID)
	if err != nil {
		if isNoRows(err) {
			return disconnectedGoogleConnectionResponse(), nil
		}
		return nil, err
	}
	return googleConnectionResponse(conn), nil
}

func (s *Service) DisconnectGoogleConnection(ctx context.Context) (*GoogleConnectionResponse, error) {
	userID, err := currentAuthUserID()
	if err != nil {
		return nil, err
	}
	conn, err := s.query.GetGoogleConnectionByUser(ctx, userID)
	if err != nil {
		if isNoRows(err) {
			return disconnectedGoogleConnectionResponse(), nil
		}
		return nil, err
	}
	if token, err := openGoogleToken(conn.RefreshTokenCiphertext); err == nil && token != "" {
		_ = revokeGoogleToken(ctx, token)
	}
	conn, err = s.query.DisconnectGoogleConnection(ctx, userID)
	if err != nil {
		return nil, err
	}
	return googleConnectionResponse(conn), nil
}

func (s *Service) finishGoogleConnection(ctx context.Context, userID authdb.UUID, claims *googleIDClaims, tokenResponse googleTokenResponse) error {
	if strings.TrimSpace(tokenResponse.AccessToken) == "" {
		return invalidArgument("google access token missing")
	}
	normalizedEmail, err := normalizeEmail(claims.Email)
	if err != nil {
		return invalidArgument(err.Error())
	}
	tx, q, err := s.beginTx(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	user, err := q.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if user.DisabledAt.Valid {
		return permissionDenied("user is disabled")
	}
	if identity, err := q.GetAuthIdentityByUserProvider(ctx, authdb.GetAuthIdentityByUserProviderParams{
		UserID:   user.ID,
		Provider: identityProviderGoogle,
	}); err == nil && strings.TrimSpace(identity.ProviderSubject) != strings.TrimSpace(claims.Subject) {
		return failedPrecondition("user is already linked to a different Google account")
	} else if err != nil && !isNoRows(err) {
		return err
	}
	if identity, err := q.GetAuthIdentityByProviderSubject(ctx, authdb.GetAuthIdentityByProviderSubjectParams{
		Provider:        identityProviderGoogle,
		ProviderSubject: strings.TrimSpace(claims.Subject),
	}); err == nil && uuidString(identity.UserID) != uuidString(user.ID) {
		return failedPrecondition("Google account is already linked to another user")
	} else if err != nil && !isNoRows(err) {
		return err
	} else if isNoRows(err) {
		identityID, idErr := newUUID()
		if idErr != nil {
			return idErr
		}
		if _, err := q.CreateAuthIdentity(ctx, authdb.CreateAuthIdentityParams{
			ID:              identityID,
			UserID:          user.ID,
			Provider:        identityProviderGoogle,
			ProviderSubject: strings.TrimSpace(claims.Subject),
			Email:           strings.TrimSpace(claims.Email),
			NormalizedEmail: normalizedEmail,
		}); err != nil {
			return err
		}
	}
	if _, err := q.UpdateUserProfileFromProvider(ctx, authdb.UpdateUserProfileFromProviderParams{
		ID:          user.ID,
		DisplayName: strings.TrimSpace(claims.Name),
		AvatarUrl:   strings.TrimSpace(claims.Picture),
	}); err != nil {
		return err
	}

	var oldRefresh []byte
	if existing, err := q.GetGoogleConnectionByUserForUpdate(ctx, user.ID); err == nil {
		oldRefresh = existing.RefreshTokenCiphertext
	} else if !isNoRows(err) {
		return err
	}
	refreshCipher := oldRefresh
	if strings.TrimSpace(tokenResponse.RefreshToken) != "" {
		refreshCipher, err = sealGoogleToken(tokenResponse.RefreshToken)
		if err != nil {
			return err
		}
	}
	if len(refreshCipher) == 0 {
		return failedPrecondition("Google did not return a refresh token; reconnect with consent")
	}
	accessCipher, err := sealGoogleToken(tokenResponse.AccessToken)
	if err != nil {
		return err
	}
	connectionID, err := newUUID()
	if err != nil {
		return err
	}
	if _, err := q.UpsertGoogleConnection(ctx, authdb.UpsertGoogleConnectionParams{
		ID:                     connectionID,
		UserID:                 user.ID,
		ProviderSubject:        strings.TrimSpace(claims.Subject),
		Email:                  strings.TrimSpace(claims.Email),
		Scopes:                 canonicalGoogleScopes(parseGoogleScopes(tokenResponse.Scope)),
		RefreshTokenCiphertext: refreshCipher,
		AccessTokenCiphertext:  accessCipher,
		AccessTokenExpiresAt:   timestamptz(googleTokenExpiry(s.clock(), tokenResponse.ExpiresIn)),
	}); err != nil {
		return err
	}
	s.recordEvent(ctx, q, "google_connection_connected", user.ID, authdb.UUID{}, authdb.UUID{}, authdb.UUID{}, nil)
	return tx.Commit()
}

func currentAuthUserID() (authdb.UUID, error) {
	data, err := currentAuthData()
	if err != nil {
		return authdb.UUID{}, unauthenticated("endpoint requires auth")
	}
	userID, err := parseUUID(string(data.UserID))
	if err != nil {
		return authdb.UUID{}, unauthenticated("invalid user id")
	}
	return userID, nil
}

func newGoogleOAuthMaterial() (state string, verifier string, nonce string, err error) {
	state, err = newRandomToken(32)
	if err != nil {
		return "", "", "", err
	}
	verifier, err = newRandomToken(48)
	if err != nil {
		return "", "", "", err
	}
	nonce, err = newRandomToken(24)
	if err != nil {
		return "", "", "", err
	}
	return state, verifier, nonce, nil
}

func currentHTTPRequest() *http.Request {
	if headers := requestHeaders(); headers != nil {
		if firstForwardedValue(headers.Get("X-Forwarded-Host")) == "" &&
			firstForwardedValue(headers.Get("Host")) == "" &&
			firstForwardedValue(headers.Get("X-Forwarded-Proto")) == "" &&
			firstForwardedValue(headers.Get("X-Forwarded-Prefix")) == "" &&
			firstForwardedValue(headers.Get("X-Scenery-Route-Prefix")) == "" {
			return nil
		}
		req, _ := http.NewRequest(http.MethodGet, "https://api.scenery.localhost/", nil)
		req.Header = headers.Clone()
		if host := firstForwardedValue(headers.Get("X-Forwarded-Host")); host != "" {
			req.Host = host
		} else if host := strings.TrimSpace(headers.Get("Host")); host != "" {
			req.Host = host
		}
		return req
	}
	return nil
}

func validateGoogleScopes(scopes []string) ([]string, error) {
	out := normalizeGoogleScopes(scopes)
	if len(out) == 0 {
		return nil, invalidArgument("at least one Google scope is required")
	}
	return out, nil
}

func validateGoogleAllowedScopes(scopes []string) error {
	cfg := currentStandardConfig()
	allowed := normalizeGoogleScopes(cfg.GoogleOAuth.AllowedScopes)
	if len(allowed) == 0 {
		return failedPrecondition("auth.google_oauth.allowed_scopes must include requested Google API scopes")
	}
	allowedSet := googleScopeSet(allowed)
	for _, scope := range scopes {
		if !allowedSet[scope] {
			return permissionDenied("Google scope is not allowed: " + scope)
		}
	}
	return nil
}

func normalizeGoogleScopes(scopes []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, item := range scopes {
		for _, scope := range strings.Fields(item) {
			scope = strings.TrimSpace(scope)
			if scope == "" || seen[scope] {
				continue
			}
			seen[scope] = true
			out = append(out, scope)
		}
	}
	sort.Strings(out)
	return out
}

func parseGoogleScopes(scope string) []string {
	return normalizeGoogleScopes([]string{scope})
}

func canonicalGoogleScopes(scopes []string) string {
	return strings.Join(normalizeGoogleScopes(scopes), " ")
}

func withGoogleIdentityScopes(scopes []string) []string {
	return normalizeGoogleScopes(append([]string{"openid", "email", "profile"}, scopes...))
}

func googleScopeSet(scopes []string) map[string]bool {
	out := make(map[string]bool, len(scopes))
	for _, scope := range scopes {
		out[scope] = true
	}
	return out
}

func googleConnectionHasUsableRefresh(conn authdb.SceneryAuthGoogleConnection, scopes []string) bool {
	return conn.Status == "active" && len(conn.RefreshTokenCiphertext) > 0 && googleScopesContain(parseGoogleScopes(conn.Scopes), scopes)
}

func googleScopesContain(have []string, want []string) bool {
	haveSet := googleScopeSet(have)
	for _, scope := range want {
		if !haveSet[scope] {
			return false
		}
	}
	return true
}

func googleTokenExpiry(now time.Time, expiresIn int64) time.Time {
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	return now.Add(time.Duration(expiresIn) * time.Second)
}

func googleConnectionResponse(conn authdb.SceneryAuthGoogleConnection) *GoogleConnectionResponse {
	resp := &GoogleConnectionResponse{
		Status: strings.TrimSpace(conn.Status),
		Email:  strings.TrimSpace(conn.Email),
		Scopes: parseGoogleScopes(conn.Scopes),
	}
	if resp.Status == "" {
		resp.Status = "disconnected"
	}
	if resp.Status == "disconnected" {
		resp.Email = ""
		resp.Scopes = nil
	}
	if conn.ConnectedAt.IsZero() == false {
		connectedAt := conn.ConnectedAt.UTC()
		resp.ConnectedAt = &connectedAt
	}
	if conn.LastRefreshAt.Valid {
		lastRefreshAt := conn.LastRefreshAt.Time.UTC()
		resp.LastRefreshAt = &lastRefreshAt
	}
	if resp.Status == "reauth_required" {
		resp.ReauthReason = strings.TrimSpace(conn.LastRefreshError)
	}
	return resp
}

func disconnectedGoogleConnectionResponse() *GoogleConnectionResponse {
	return &GoogleConnectionResponse{Status: "disconnected"}
}

func redirectGoogleConnectionCallbackError(w http.ResponseWriter, req *http.Request, redirectPath string, code string) {
	http.Redirect(w, req, appRedirectURL(req, redirectPathWithQuery(safeRedirectPath(redirectPath), "error", code)), http.StatusFound)
}

func consumeGoogleConnectionErrorRedirectPath(req *http.Request) (string, bool) {
	state := strings.TrimSpace(req.URL.Query().Get("state"))
	if state == "" {
		return "/", false
	}
	svc, err := standardAuthService(req.Context())
	if err != nil {
		return "/", false
	}
	oauthState, err := svc.query.ConsumeOAuthState(req.Context(), tokenHash(state))
	if err != nil {
		return "/", false
	}
	if strings.TrimSpace(oauthState.Purpose) != googleConnectionOAuthPurpose || !oauthState.UserID.Valid {
		return "/", false
	}
	return safeRedirectPath(oauthState.RedirectPath), true
}

func redirectPathWithQuery(path string, key string, value string) string {
	u, err := url.Parse(safeRedirectPath(path))
	if err != nil {
		return "/"
	}
	query := u.Query()
	query.Set(key, value)
	u.RawQuery = query.Encode()
	return u.String()
}

func googleConnectionCallbackErrorCode(err error) string {
	switch errs.Code(err) {
	case errs.PermissionDenied:
		return "google_scope_forbidden"
	case errs.GoogleScopeMissing:
		return "google_scope_missing"
	case errs.FailedPrecondition:
		return "google_link_precondition"
	case errs.InvalidArgument:
		return "google_token"
	default:
		return "google_internal"
	}
}

func revokeGoogleToken(ctx context.Context, token string) error {
	form := url.Values{}
	form.Set("token", strings.TrimSpace(token))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, googleRevokeEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := googleHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("google revoke endpoint status %d", resp.StatusCode)
	}
	return nil
}
