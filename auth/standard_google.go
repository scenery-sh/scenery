package auth

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	authdb "scenery.sh/auth/db/gen"
)

const (
	googleAuthEndpoint  = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenEndpoint = "https://oauth2.googleapis.com/token"
	googleJWKSURL       = "https://www.googleapis.com/oauth2/v3/certs"
)

// GoogleStart starts the Google OAuth Authorization Code + PKCE flow.
func GoogleStart(w http.ResponseWriter, req *http.Request) {
	if strings.TrimSpace(secrets.GoogleOAuthClientID) == "" || strings.TrimSpace(secrets.GoogleOAuthClientSecret) == "" {
		http.Error(w, "Google OAuth is not configured", http.StatusServiceUnavailable)
		return
	}
	svc, err := newRuntimeService(req.Context())
	if err != nil {
		http.Error(w, "auth service unavailable", http.StatusInternalServerError)
		return
	}

	state, err := newRandomToken(32)
	if err != nil {
		http.Error(w, "failed to create oauth state", http.StatusInternalServerError)
		return
	}
	verifier, err := newRandomToken(48)
	if err != nil {
		http.Error(w, "failed to create oauth verifier", http.StatusInternalServerError)
		return
	}
	nonce, err := newRandomToken(24)
	if err != nil {
		http.Error(w, "failed to create oauth nonce", http.StatusInternalServerError)
		return
	}
	stateID, err := newUUID()
	if err != nil {
		http.Error(w, "failed to create oauth state", http.StatusInternalServerError)
		return
	}
	_, err = svc.query.CreateOAuthState(req.Context(), authdb.CreateOAuthStateParams{
		ID:           stateID,
		StateHash:    tokenHash(state),
		PkceVerifier: verifier,
		NonceHash:    tokenHash(nonce),
		RedirectPath: safeRedirectPath(req.URL.Query().Get("redirect_path")),
		ExpiresAt:    svc.clock().Add(defaultOAuthStateTTL),
	})
	if err != nil {
		http.Error(w, "failed to store oauth state", http.StatusInternalServerError)
		return
	}

	challengeSum := sha256.Sum256([]byte(verifier))
	authURL, _ := url.Parse(googleAuthEndpoint)
	query := authURL.Query()
	query.Set("client_id", strings.TrimSpace(secrets.GoogleOAuthClientID))
	query.Set("redirect_uri", googleRedirectURI(req))
	query.Set("response_type", "code")
	query.Set("scope", "openid email profile")
	query.Set("state", state)
	query.Set("nonce", nonce)
	query.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challengeSum[:]))
	query.Set("code_challenge_method", "S256")
	query.Set("prompt", "select_account")
	authURL.RawQuery = query.Encode()
	http.Redirect(w, req, authURL.String(), http.StatusFound)
}

// GoogleCallback completes the Google OAuth flow, sets the refresh cookie, and redirects to the app.
func GoogleCallback(w http.ResponseWriter, req *http.Request) {
	if oauthErr := strings.TrimSpace(req.URL.Query().Get("error")); oauthErr != "" {
		http.Redirect(w, req, appRedirectURL("/sign-in?error=google_oauth"), http.StatusFound)
		return
	}
	state := strings.TrimSpace(req.URL.Query().Get("state"))
	code := strings.TrimSpace(req.URL.Query().Get("code"))
	if state == "" || code == "" {
		http.Error(w, "missing oauth callback parameters", http.StatusBadRequest)
		return
	}

	svc, err := newRuntimeService(req.Context())
	if err != nil {
		http.Error(w, "auth service unavailable", http.StatusInternalServerError)
		return
	}
	oauthState, err := svc.query.ConsumeOAuthState(req.Context(), tokenHash(state))
	if err != nil {
		http.Error(w, "oauth state is invalid or expired", http.StatusBadRequest)
		return
	}
	tokenResponse, err := exchangeGoogleCode(req.Context(), code, oauthState.PkceVerifier, googleRedirectURI(req))
	if err != nil {
		http.Error(w, "failed to exchange google code", http.StatusBadGateway)
		return
	}
	claims, err := verifyGoogleIDToken(req.Context(), tokenResponse.IDToken)
	if err != nil {
		http.Error(w, "google id token is invalid", http.StatusUnauthorized)
		return
	}
	if !claims.EmailVerified {
		http.Error(w, "google email is not verified", http.StatusForbidden)
		return
	}
	if oauthState.NonceHash != "" && tokenHash(claims.Nonce) != oauthState.NonceHash {
		http.Error(w, "google nonce mismatch", http.StatusUnauthorized)
		return
	}

	response, err := svc.finishGoogleSignIn(req.Context(), claims)
	if err != nil {
		http.Error(w, "failed to finish google sign-in", http.StatusInternalServerError)
		return
	}
	w.Header().Add("Set-Cookie", response.SetCookie)
	http.Redirect(w, req, appRedirectURL(oauthState.RedirectPath), http.StatusFound)
}

func newRuntimeService(ctx context.Context) (*Service, error) {
	return standardAuthService(ctx)
}

type googleTokenResponse struct {
	IDToken string `json:"id_token"`
}

func exchangeGoogleCode(ctx context.Context, code string, verifier string, redirectURI string) (googleTokenResponse, error) {
	form := url.Values{}
	form.Set("code", code)
	form.Set("client_id", strings.TrimSpace(secrets.GoogleOAuthClientID))
	form.Set("client_secret", strings.TrimSpace(secrets.GoogleOAuthClientSecret))
	form.Set("redirect_uri", redirectURI)
	form.Set("grant_type", "authorization_code")
	form.Set("code_verifier", verifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, googleTokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return googleTokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return googleTokenResponse{}, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return googleTokenResponse{}, fmt.Errorf("google token endpoint status %d", resp.StatusCode)
	}
	var out googleTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return googleTokenResponse{}, err
	}
	if strings.TrimSpace(out.IDToken) == "" {
		return googleTokenResponse{}, fmt.Errorf("google id_token missing")
	}
	return out, nil
}

type googleIDClaims struct {
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`
	Nonce         string `json:"nonce"`
	jwt.RegisteredClaims
}

func verifyGoogleIDToken(ctx context.Context, rawIDToken string) (*googleIDClaims, error) {
	keys, err := fetchGoogleKeys(ctx)
	if err != nil {
		return nil, err
	}
	claims := &googleIDClaims{}
	token, err := jwt.ParseWithClaims(
		rawIDToken,
		claims,
		func(t *jwt.Token) (any, error) {
			if t.Method.Alg() != jwt.SigningMethodRS256.Alg() {
				return nil, fmt.Errorf("unexpected google signing method")
			}
			kid, _ := t.Header["kid"].(string)
			key := keys[strings.TrimSpace(kid)]
			if key == nil {
				return nil, fmt.Errorf("google signing key not found")
			}
			return key, nil
		},
		jwt.WithExpirationRequired(),
		jwt.WithAudience(strings.TrimSpace(secrets.GoogleOAuthClientID)),
	)
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, fmt.Errorf("invalid google id token")
	}
	issuer := strings.TrimSpace(claims.Issuer)
	if issuer != "accounts.google.com" && issuer != "https://accounts.google.com" {
		return nil, fmt.Errorf("invalid google issuer")
	}
	if strings.TrimSpace(claims.Subject) == "" {
		return nil, fmt.Errorf("google subject missing")
	}
	return claims, nil
}

type jwksResponse struct {
	Keys []struct {
		Kid string `json:"kid"`
		Kty string `json:"kty"`
		Alg string `json:"alg"`
		Use string `json:"use"`
		N   string `json:"n"`
		E   string `json:"e"`
	} `json:"keys"`
}

func fetchGoogleKeys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, googleJWKSURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("google jwks status %d", resp.StatusCode)
	}
	var payload jwksResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	out := make(map[string]*rsa.PublicKey, len(payload.Keys))
	for _, key := range payload.Keys {
		if key.Kty != "RSA" || strings.TrimSpace(key.Kid) == "" {
			continue
		}
		nBytes, err := base64.RawURLEncoding.DecodeString(key.N)
		if err != nil {
			continue
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(key.E)
		if err != nil {
			continue
		}
		exponent := 0
		for _, b := range eBytes {
			exponent = exponent<<8 + int(b)
		}
		if exponent == 0 {
			continue
		}
		out[key.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: exponent}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no google keys")
	}
	return out, nil
}

func (s *Service) finishGoogleSignIn(ctx context.Context, claims *googleIDClaims) (*AuthSessionResponse, error) {
	normalizedEmail, err := normalizeEmail(claims.Email)
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

	var user authdb.SceneryAuthUser
	identity, err := q.GetAuthIdentityByProviderSubject(ctx, authdb.GetAuthIdentityByProviderSubjectParams{
		Provider:        identityProviderGoogle,
		ProviderSubject: strings.TrimSpace(claims.Subject),
	})
	if err == nil {
		user, err = q.GetUserByID(ctx, identity.UserID)
		if err != nil {
			return nil, err
		}
		user, err = q.UpdateUserProfileFromProvider(ctx, authdb.UpdateUserProfileFromProviderParams{
			ID:          user.ID,
			DisplayName: strings.TrimSpace(claims.Name),
			AvatarUrl:   strings.TrimSpace(claims.Picture),
		})
		if err != nil {
			return nil, err
		}
	} else if isNoRows(err) {
		user, err = q.GetUserByNormalizedEmail(ctx, normalizedEmail)
		if err != nil {
			if !isNoRows(err) {
				return nil, err
			}
			userID, idErr := newUUID()
			if idErr != nil {
				return nil, idErr
			}
			user, err = q.CreateUser(ctx, authdb.CreateUserParams{
				ID:                     userID,
				DisplayName:            defaultDisplayName(normalizedEmail, claims.Name),
				AvatarUrl:              strings.TrimSpace(claims.Picture),
				PrimaryEmail:           strings.TrimSpace(claims.Email),
				NormalizedPrimaryEmail: normalizedEmail,
				EmailVerifiedAt:        sql.NullTime{Time: s.clock(), Valid: true},
			})
			if err != nil {
				return nil, err
			}
		} else if !user.EmailVerifiedAt.Valid {
			return nil, failedPrecondition("verify the email/password account before linking Google")
		}
		identityID, idErr := newUUID()
		if idErr != nil {
			return nil, idErr
		}
		if _, err := q.CreateAuthIdentity(ctx, authdb.CreateAuthIdentityParams{
			ID:              identityID,
			UserID:          user.ID,
			Provider:        identityProviderGoogle,
			ProviderSubject: strings.TrimSpace(claims.Subject),
			Email:           strings.TrimSpace(claims.Email),
			NormalizedEmail: normalizedEmail,
		}); err != nil {
			return nil, err
		}
	} else {
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
	s.recordEvent(ctx, q, "login_google", user.ID, authdb.UUID{}, tenantID, authdb.UUID{}, nil)
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return response, nil
}

func googleRedirectURI(req *http.Request) string {
	base := strings.TrimRight(strings.TrimSpace(secrets.APIBaseURL), "/")
	if base == "" && req != nil {
		scheme := strings.TrimSpace(req.Header.Get("X-Forwarded-Proto"))
		if scheme == "" {
			scheme = "https"
			if isLocalRuntime() {
				scheme = "http"
			}
		}
		host := strings.TrimSpace(req.Host)
		if host != "" {
			base = scheme + "://" + host
		}
	}
	if base == "" {
		base = "https://api.scenery.localhost"
	}
	return base + "/auth/google/callback"
}

func appRedirectURL(path string) string {
	base := strings.TrimRight(strings.TrimSpace(secrets.PublicAppURL), "/")
	if base == "" {
		base = "https://app.scenery.localhost"
	}
	return base + safeRedirectPath(path)
}
