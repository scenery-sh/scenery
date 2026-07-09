package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	authdb "scenery.sh/auth/db/gen"
	"scenery.sh/errs"
	"scenery.sh/runtime"
)

const gmailModifyScope = "https://www.googleapis.com/auth/gmail.modify"
const calendarEventsScope = "https://www.googleapis.com/auth/calendar.events"

func TestGoogleOAuthBrowserFlowWithFakeGoogle(t *testing.T) {
	ctx := t.Context()
	databaseURL, cleanup := createAuthLiveTestDatabase(t, ctx)
	t.Cleanup(cleanup)
	resetStandardAuthStateForTest(t)

	fake := newFakeGoogleServer(t)
	fake.email = "person@example.test"
	fake.emailVerified = true
	fake.subject = "happy-subject"
	overrideGoogleEndpointsForTest(t, fake)
	t.Setenv("DatabaseURL", databaseURL)
	t.Setenv("JWTSecret", "test-jwt-secret")
	t.Setenv("GoogleOAuthClientID", "client-id")
	t.Setenv("GoogleOAuthClientSecret", "client-secret")
	t.Setenv("PublicAppURL", "https://app.example.test")
	runtime.SetAppConfig(runtime.AppConfig{Name: "google-oauth-test", ListenAddr: "127.0.0.1:0"})

	cfg := normalizeStandardConfig(StandardConfig{
		Enabled: true,
		GoogleOAuth: GoogleOAuthConfig{
			Enabled: true,
		},
		AutoBootstrapDatabase: true,
	})
	applyStandardSecrets(cfg)
	standardAuthState.mu.Lock()
	standardAuthState.cfg = cfg
	standardAuthState.mu.Unlock()
	if _, ok := runtime.LookupEndpoint("auth", "Me"); !ok {
		registerStandardAuthEndpoints(cfg)
	}

	callbackReq, callbackRec := runFakeGoogleFlow(t, fake, "/welcome")
	assertRedirect(t, callbackRec, "https://app.example.test/welcome")
	refreshCookie := findCookie(callbackRec.Result().Cookies(), refreshCookieName)
	if refreshCookie == nil || strings.TrimSpace(refreshCookie.Value) == "" {
		t.Fatalf("callback did not set refresh cookie %q", refreshCookieName)
	}

	svc, err := standardAuthService(ctx)
	if err != nil {
		t.Fatalf("standard auth service: %v", err)
	}
	authData := assertRefreshCookieValid(t, svc, refreshCookie.Value)
	assertMeEndpointBootstrap(t, authData)

	replayRec := httptest.NewRecorder()
	GoogleCallback(replayRec, callbackReq)
	assertRedirect(t, replayRec, "https://app.example.test/sign-in?error=oauth_state")

	verifiedUserID := signupEmailUser(t, svc, "linked@example.test", true)
	fake.subject = "linked-google-subject"
	fake.email = "linked@example.test"
	fake.emailVerified = true
	fake.nonceOverride = ""
	_, linkedRec := runFakeGoogleFlow(t, fake, "/")
	assertRedirect(t, linkedRec, "https://app.example.test/")
	identity, err := svc.query.GetAuthIdentityByProviderSubject(ctx, authdb.GetAuthIdentityByProviderSubjectParams{
		Provider:        identityProviderGoogle,
		ProviderSubject: "linked-google-subject",
	})
	if err != nil {
		t.Fatalf("linked google identity: %v", err)
	}
	if uuidString(identity.UserID) != verifiedUserID {
		t.Fatalf("linked google identity user = %s, want %s", uuidString(identity.UserID), verifiedUserID)
	}

	signupEmailUser(t, svc, "unverified@example.test", false)
	fake.subject = "unverified-google-subject"
	fake.email = "unverified@example.test"
	fake.emailVerified = true
	_, unverifiedRec := runFakeGoogleFlow(t, fake, "/")
	assertRedirect(t, unverifiedRec, "https://app.example.test/sign-in?error=google_link_precondition")
	if _, err := svc.query.GetAuthIdentityByProviderSubject(ctx, authdb.GetAuthIdentityByProviderSubjectParams{
		Provider:        identityProviderGoogle,
		ProviderSubject: "unverified-google-subject",
	}); !isNoRows(err) {
		t.Fatalf("unverified link identity err = %v, want no rows", err)
	}

	fake.subject = "nonce-mismatch-subject"
	fake.email = "nonce@example.test"
	fake.emailVerified = true
	fake.nonceOverride = "wrong-nonce"
	_, nonceRec := runFakeGoogleFlow(t, fake, "/")
	assertRedirect(t, nonceRec, "https://app.example.test/sign-in?error=google_id_token")
	fake.nonceOverride = ""

	fake.subject = "unverified-google-email-subject"
	fake.email = "email-unverified@example.test"
	fake.emailVerified = false
	_, emailUnverifiedRec := runFakeGoogleFlow(t, fake, "/")
	assertRedirect(t, emailUnverifiedRec, "https://app.example.test/sign-in?error=google_email_unverified")
}

func runFakeGoogleFlow(t *testing.T, fake *fakeGoogleServer, redirectPath string) (*http.Request, *httptest.ResponseRecorder) {
	t.Helper()
	startReq := httptest.NewRequest(http.MethodGet, "http://api.example.test/auth/google/start?redirect_path="+url.QueryEscape(redirectPath), nil)
	startRec := httptest.NewRecorder()
	GoogleStart(startRec, startReq)
	if startRec.Code != http.StatusFound {
		t.Fatalf("start status = %d, want %d: %s", startRec.Code, http.StatusFound, startRec.Body.String())
	}

	callbackURL := fake.authorize(t, startRec.Header().Get("Location"))
	callbackReq := httptest.NewRequest(http.MethodGet, callbackURL, nil)
	callbackRec := httptest.NewRecorder()
	GoogleCallback(callbackRec, callbackReq)
	if callbackRec.Code != http.StatusFound {
		t.Fatalf("callback status = %d, want %d: %s", callbackRec.Code, http.StatusFound, callbackRec.Body.String())
	}
	return callbackReq, callbackRec
}

func signupEmailUser(t *testing.T, svc *Service, email string, verify bool) string {
	t.Helper()
	ctx := t.Context()
	signup, err := svc.SignupEmail(ctx, &EmailSignupParams{
		Email:       email,
		Password:    "correct horse battery staple",
		DisplayName: "Linked User",
	})
	if err != nil {
		t.Fatalf("signup %s: %v", email, err)
	}
	if verify {
		if strings.TrimSpace(signup.DevVerificationToken) == "" {
			t.Fatalf("signup %s did not return local dev verification token", email)
		}
		if _, err := svc.ConfirmEmailVerification(ctx, &EmailVerificationConfirmParams{Token: signup.DevVerificationToken}); err != nil {
			t.Fatalf("verify %s: %v", email, err)
		}
	}
	normalized, err := normalizeEmail(email)
	if err != nil {
		t.Fatal(err)
	}
	user, err := svc.query.GetUserByNormalizedEmail(ctx, normalized)
	if err != nil {
		t.Fatalf("get user %s: %v", email, err)
	}
	return uuidString(user.ID)
}

func assertRefreshCookieValid(t *testing.T, svc *Service, refreshToken string) *AuthData {
	t.Helper()
	session, err := svc.Refresh(t.Context(), &RefreshParams{RefreshToken: refreshToken})
	if err != nil {
		t.Fatalf("refresh callback cookie: %v", err)
	}
	authData, err := ValidateToken(session.Token)
	if err != nil {
		t.Fatalf("validate refreshed access token: %v", err)
	}
	if strings.TrimSpace(string(authData.UserID)) == "" || strings.TrimSpace(string(authData.TenantID)) == "" {
		t.Fatalf("refreshed auth data missing user/tenant: %+v", authData)
	}
	return authData
}

func assertMeEndpointBootstrap(t *testing.T, authData *AuthData) {
	t.Helper()
	resp, err := runtime.CallEndpoint(WithContext(t.Context(), UID(authData.UserID), authData), "auth", "Me", nil, nil)
	if err != nil {
		t.Fatalf("/auth/me endpoint after google sign-in: %v", err)
	}
	me, ok := resp.(*AuthBootstrapResponse)
	if !ok {
		t.Fatalf("/auth/me response type = %T, want *AuthBootstrapResponse", resp)
	}
	if me.User.ID != string(authData.UserID) || me.CurrentTenantID != string(authData.TenantID) {
		t.Fatalf("/auth/me bootstrap = user %q tenant %q, want user %q tenant %q", me.User.ID, me.CurrentTenantID, authData.UserID, authData.TenantID)
	}
}

func assertRedirect(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusFound, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != want {
		t.Fatalf("redirect = %q, want %q", got, want)
	}
}

func TestVerifyGoogleIDTokenCachesJWKSAndRefetchesUnknownKID(t *testing.T) {
	oldURL := googleJWKSURL
	oldSecrets := secrets
	resetGoogleJWKSCacheForTest()
	t.Cleanup(func() {
		googleJWKSURL = oldURL
		secrets = oldSecrets
		resetGoogleJWKSCacheForTest()
	})

	secrets.GoogleOAuthClientID = "client-id"
	firstKey := mustRSAKey(t)
	secondKey := mustRSAKey(t)
	keys := []testJWK{{kid: "kid-1", key: &firstKey.PublicKey}}
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writeTestJWKS(t, w, keys)
	}))
	t.Cleanup(server.Close)
	googleJWKSURL = server.URL

	if _, err := verifyGoogleIDToken(t.Context(), mustGoogleIDToken(t, firstKey, "kid-1")); err != nil {
		t.Fatalf("verify first token: %v", err)
	}
	keys = []testJWK{{kid: "kid-2", key: &secondKey.PublicKey}}
	if _, err := verifyGoogleIDToken(t.Context(), mustGoogleIDToken(t, firstKey, "kid-1")); err != nil {
		t.Fatalf("verify cached token: %v", err)
	}
	if _, err := verifyGoogleIDToken(t.Context(), mustGoogleIDToken(t, secondKey, "kid-2")); err != nil {
		t.Fatalf("verify token after unknown kid refetch: %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("jwks requests = %d, want 2", got)
	}
}

func TestGoogleAppRedirectUsesRequestOriginBeforeConfiguredPublicAppURL(t *testing.T) {
	oldSecrets := secrets
	t.Cleanup(func() { secrets = oldSecrets })
	secrets.PublicAppURL = "https://blog.example.test"

	req := httptest.NewRequest(http.MethodGet, "http://local.clean.tech/api/auth/google/callback", nil)
	req.Host = "local.clean.tech"
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	redirectGoogleCallbackError(rec, req, "google_token")

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}
	if got, want := rec.Header().Get("Location"), "https://local.clean.tech/sign-in?error=google_token"; got != want {
		t.Fatalf("location = %q, want %q", got, want)
	}
	if got, want := appRedirectURL(req, "/next/"), "https://local.clean.tech/next/"; got != want {
		t.Fatalf("app redirect URL = %q, want %q", got, want)
	}
}

func TestGoogleRedirectURIUsesRequestHostBeforeConfiguredPathModeURL(t *testing.T) {
	oldSecrets := secrets
	t.Cleanup(func() { secrets = oldSecrets })
	secrets.APIBaseURL = "http://localhost:4747/api"

	req := httptest.NewRequest(http.MethodGet, "http://local.clean.tech/api/auth/google/start", nil)
	req.Host = "local.clean.tech"
	req.Header.Set("X-Forwarded-Prefix", "/api")
	req.Header.Set("X-Forwarded-Proto", "https")

	if got, want := googleRedirectURI(req), "https://local.clean.tech/api/auth/google/callback"; got != want {
		t.Fatalf("redirect URI = %q, want %q", got, want)
	}
}

func TestGoogleConnectionStartFallsBackToConfiguredAPIBaseURL(t *testing.T) {
	svc, fake, authData := setupGoogleConnectionTest(t)
	_ = svc

	resp, err := runtime.CallEndpoint(
		WithContext(t.Context(), UID(authData.UserID), authData),
		"auth",
		"GoogleConnectStart",
		nil,
		&GoogleConnectStartParams{Scopes: []string{gmailModifyScope}},
	)
	if err != nil {
		t.Fatalf("GoogleConnectStart endpoint: %v", err)
	}
	start := resp.(*GoogleConnectStartResponse)
	authURL, err := url.Parse(start.AuthorizeURL)
	if err != nil {
		t.Fatalf("authorize url: %v", err)
	}
	if got, want := authURL.Query().Get("redirect_uri"), "https://api.example.test/auth/google/callback"; got != want {
		t.Fatalf("redirect_uri = %q, want %q", got, want)
	}
	if got := authURL.String(); !strings.HasPrefix(got, fake.server.URL+"/auth") {
		t.Fatalf("authorize url = %q, want fake Google host", got)
	}
}

func TestGoogleConnectionFlowStoresEncryptedTokenAndDisconnects(t *testing.T) {
	svc, fake, authData := setupGoogleConnectionTest(t)
	fake.accessToken = "initial-access"
	fake.refreshToken = "initial-refresh"
	fake.scope = gmailModifyScope

	rec := runFakeGoogleConnectionFlow(t, fake, authData, "/settings")
	if got := rec.Header().Get("Location"); !strings.Contains(got, "google_connected=1") {
		t.Fatalf("connection callback redirect = %q, want google_connected=1", got)
	}

	conn, err := svc.query.GetGoogleConnectionByUser(t.Context(), mustParseAuthUUID(t, string(authData.UserID)))
	if err != nil {
		t.Fatalf("get google connection: %v", err)
	}
	if conn.Status != "active" || conn.Email != "person@example.test" || !googleScopesContain(parseGoogleScopes(conn.Scopes), []string{gmailModifyScope}) {
		t.Fatalf("connection = %+v", conn)
	}
	if strings.Contains(string(conn.RefreshTokenCiphertext), "initial-refresh") {
		t.Fatal("refresh token was stored in plaintext")
	}
	opened, err := openGoogleToken(conn.RefreshTokenCiphertext)
	if err != nil || opened != "initial-refresh" {
		t.Fatalf("open refresh token = %q, %v", opened, err)
	}

	status, err := runtime.CallEndpoint(WithContext(t.Context(), UID(authData.UserID), authData), "auth", "GetGoogleConnection", nil, nil)
	if err != nil {
		t.Fatalf("GetGoogleConnection endpoint: %v", err)
	}
	if got := status.(*GoogleConnectionResponse); got.Status != "active" || !googleScopesContain(got.Scopes, []string{gmailModifyScope}) {
		t.Fatalf("status = %+v", got)
	}
	disc, err := runtime.CallEndpoint(WithContext(t.Context(), UID(authData.UserID), authData), "auth", "DisconnectGoogleConnection", nil, nil)
	if err != nil {
		t.Fatalf("DisconnectGoogleConnection endpoint: %v", err)
	}
	if got := disc.(*GoogleConnectionResponse); got.Status != "disconnected" {
		t.Fatalf("disconnect status = %+v", got)
	}
	if fake.revokeCalls.Load() != 1 {
		t.Fatalf("revoke calls = %d, want 1", fake.revokeCalls.Load())
	}
}

func TestGoogleConnectionCallbackOAuthErrorUsesStateRedirect(t *testing.T) {
	_, _, authData := setupGoogleConnectionTest(t)

	resp, err := runtime.CallEndpoint(
		WithContext(t.Context(), UID(authData.UserID), authData),
		"auth",
		"GoogleConnectStart",
		nil,
		&GoogleConnectStartParams{Scopes: []string{gmailModifyScope}, RedirectPath: "/settings"},
	)
	if err != nil {
		t.Fatalf("GoogleConnectStart endpoint: %v", err)
	}
	start := resp.(*GoogleConnectStartResponse)
	authURL, err := url.Parse(start.AuthorizeURL)
	if err != nil {
		t.Fatalf("authorize url: %v", err)
	}
	callbackReq := httptest.NewRequest(http.MethodGet, "https://api.example.test/auth/google/callback?error=access_denied&state="+url.QueryEscape(authURL.Query().Get("state")), nil)
	callbackRec := httptest.NewRecorder()
	GoogleCallback(callbackRec, callbackReq)
	assertRedirect(t, callbackRec, "https://app.example.test/settings?error=google_oauth")
}

func TestGoogleAccessTokenRefreshesRotatesAndSingleFlights(t *testing.T) {
	svc, fake, authData := setupGoogleConnectionTest(t)
	fake.accessToken = "initial-access"
	fake.refreshToken = "initial-refresh"
	fake.scope = gmailModifyScope
	runFakeGoogleConnectionFlow(t, fake, authData, "/")
	userID := mustParseAuthUUID(t, string(authData.UserID))

	token, err := GoogleAccessTokenForUser(t.Context(), string(authData.UserID), gmailModifyScope)
	if err != nil || token != "initial-access" {
		t.Fatalf("cached GoogleAccessTokenForUser = %q, %v", token, err)
	}
	if _, err := svc.db.ExecContext(t.Context(), `update scenery.scenery_auth_google_connections set access_token_expires_at = now() - interval '1 minute' where user_id = $1`, userID); err != nil {
		t.Fatalf("expire access token: %v", err)
	}
	fake.mu.Lock()
	fake.refreshQueue = append(fake.refreshQueue, fakeGoogleRefreshResponse{
		accessToken:  "rotated-access",
		refreshToken: "rotated-refresh",
		scope:        gmailModifyScope,
	})
	fake.mu.Unlock()

	var wg sync.WaitGroup
	results := make(chan string, 2)
	errsCh := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := GoogleAccessTokenForUser(t.Context(), string(authData.UserID), gmailModifyScope)
			if err != nil {
				errsCh <- err
				return
			}
			results <- token
		}()
	}
	wg.Wait()
	close(results)
	close(errsCh)
	for err := range errsCh {
		t.Fatalf("concurrent GoogleAccessTokenForUser: %v", err)
	}
	for token := range results {
		if token != "rotated-access" {
			t.Fatalf("concurrent token = %q, want rotated-access", token)
		}
	}
	if got := fake.refreshCalls.Load(); got != 1 {
		t.Fatalf("refresh calls = %d, want 1", got)
	}
	conn, err := svc.query.GetGoogleConnectionByUser(t.Context(), userID)
	if err != nil {
		t.Fatalf("get connection after rotation: %v", err)
	}
	opened, err := openGoogleToken(conn.RefreshTokenCiphertext)
	if err != nil || opened != "rotated-refresh" {
		t.Fatalf("stored refresh after rotation = %q, %v", opened, err)
	}
}

func TestGoogleAccessTokenRetriesTransientAndMarksPermanentRefreshFailures(t *testing.T) {
	svc, fake, authData := setupGoogleConnectionTest(t)
	fake.accessToken = "initial-access"
	fake.refreshToken = "initial-refresh"
	fake.scope = gmailModifyScope
	runFakeGoogleConnectionFlow(t, fake, authData, "/")
	userID := mustParseAuthUUID(t, string(authData.UserID))
	if _, err := svc.db.ExecContext(t.Context(), `update scenery.scenery_auth_google_connections set access_token_expires_at = now() - interval '1 minute' where user_id = $1`, userID); err != nil {
		t.Fatalf("expire access token: %v", err)
	}
	fake.mu.Lock()
	fake.refreshQueue = append(fake.refreshQueue,
		fakeGoogleRefreshResponse{status: http.StatusInternalServerError, errorCode: "server_error"},
		fakeGoogleRefreshResponse{accessToken: "retried-access", scope: gmailModifyScope},
	)
	fake.mu.Unlock()
	token, err := GoogleAccessTokenForUser(t.Context(), string(authData.UserID), gmailModifyScope)
	if err != nil || token != "retried-access" {
		t.Fatalf("retried refresh token = %q, %v", token, err)
	}
	if got := fake.refreshCalls.Load(); got != 2 {
		t.Fatalf("refresh calls after retry = %d, want 2", got)
	}

	if _, err := svc.db.ExecContext(t.Context(), `update scenery.scenery_auth_google_connections set access_token_expires_at = now() - interval '1 minute' where user_id = $1`, userID); err != nil {
		t.Fatalf("expire access token again: %v", err)
	}
	fake.mu.Lock()
	fake.refreshQueue = append(fake.refreshQueue, fakeGoogleRefreshResponse{status: http.StatusBadRequest, errorCode: "invalid_grant"})
	fake.mu.Unlock()
	_, err = GoogleAccessTokenForUser(t.Context(), string(authData.UserID), gmailModifyScope)
	if errs.Code(err) != errs.GoogleReauthRequired {
		t.Fatalf("permanent refresh err = %v, code %q", err, errs.Code(err))
	}
	conn, err := svc.query.GetGoogleConnectionByUser(t.Context(), userID)
	if err != nil {
		t.Fatalf("get connection after invalid_grant: %v", err)
	}
	if conn.Status != "reauth_required" || conn.LastRefreshError != "invalid_grant" {
		t.Fatalf("connection after invalid_grant = %+v", conn)
	}
	before := fake.refreshCalls.Load()
	_, err = GoogleAccessTokenForUser(t.Context(), string(authData.UserID), gmailModifyScope)
	if errs.Code(err) != errs.GoogleReauthRequired {
		t.Fatalf("reauth-required fast fail err = %v, code %q", err, errs.Code(err))
	}
	if fake.refreshCalls.Load() != before {
		t.Fatalf("reauth-required call hit Google: before=%d after=%d", before, fake.refreshCalls.Load())
	}
}

func TestGoogleAccessTokenReportsMissingScopes(t *testing.T) {
	_, fake, authData := setupGoogleConnectionTest(t)
	allowGoogleScopeForTest("https://www.googleapis.com/auth/gmail.readonly")
	fake.accessToken = "initial-access"
	fake.refreshToken = "initial-refresh"
	fake.scope = gmailModifyScope
	runFakeGoogleConnectionFlow(t, fake, authData, "/")

	_, err := GoogleAccessTokenForUser(t.Context(), string(authData.UserID), "https://www.googleapis.com/auth/gmail.readonly")
	if errs.Code(err) != errs.GoogleScopeMissing {
		t.Fatalf("missing scope err = %v, code %q", err, errs.Code(err))
	}
}

func TestGoogleAccessTokenEnforcesAllowedScopes(t *testing.T) {
	_, fake, authData := setupGoogleConnectionTest(t)
	fake.accessToken = "initial-access"
	fake.refreshToken = "initial-refresh"
	fake.scope = gmailModifyScope + " " + calendarEventsScope
	runFakeGoogleConnectionFlow(t, fake, authData, "/")

	_, err := GoogleAccessTokenForUser(t.Context(), string(authData.UserID), calendarEventsScope)
	if errs.Code(err) != errs.PermissionDenied {
		t.Fatalf("disallowed scope err = %v, code %q", err, errs.Code(err))
	}
}

func TestGoogleTokenCipherRoundTrip(t *testing.T) {
	resetStandardAuthStateForTest(t)
	key := base64.StdEncoding.EncodeToString([]byte("12345678901234567890123456789012"))
	t.Setenv("AuthTokenCipherKey", key)
	standardAuthState.mu.Lock()
	standardAuthState.cfg = normalizeStandardConfig(StandardConfig{Enabled: true, GoogleOAuth: GoogleOAuthConfig{Enabled: true}})
	standardAuthState.mu.Unlock()

	ciphertext, err := sealGoogleToken("refresh-secret")
	if err != nil {
		t.Fatalf("sealGoogleToken: %v", err)
	}
	if strings.Contains(string(ciphertext), "refresh-secret") {
		t.Fatal("ciphertext contains plaintext")
	}
	opened, err := openGoogleToken(ciphertext)
	if err != nil || opened != "refresh-secret" {
		t.Fatalf("openGoogleToken = %q, %v", opened, err)
	}
}

func setupGoogleConnectionTest(t *testing.T) (*Service, *fakeGoogleServer, *AuthData) {
	t.Helper()
	ctx := t.Context()
	databaseURL, cleanup := createAuthLiveTestDatabase(t, ctx)
	t.Cleanup(cleanup)
	resetStandardAuthStateForTest(t)

	oldBackoff := googleRefreshRetryBackoff
	googleRefreshRetryBackoff = time.Millisecond
	t.Cleanup(func() { googleRefreshRetryBackoff = oldBackoff })

	fake := newFakeGoogleServer(t)
	fake.email = "person@example.test"
	fake.emailVerified = true
	fake.subject = "connection-subject"
	overrideGoogleEndpointsForTest(t, fake)
	t.Setenv("DatabaseURL", databaseURL)
	t.Setenv("JWTSecret", "test-jwt-secret")
	t.Setenv("AuthTokenCipherKey", base64.StdEncoding.EncodeToString([]byte("12345678901234567890123456789012")))
	t.Setenv("GoogleOAuthClientID", "client-id")
	t.Setenv("GoogleOAuthClientSecret", "client-secret")
	t.Setenv("PublicAppURL", "https://app.example.test")
	t.Setenv("APIBaseURL", "https://api.example.test")
	runtime.SetAppConfig(runtime.AppConfig{Name: "google-connection-test", ListenAddr: "127.0.0.1:0"})

	cfg := normalizeStandardConfig(StandardConfig{
		Enabled: true,
		GoogleOAuth: GoogleOAuthConfig{
			Enabled:       true,
			AllowedScopes: []string{gmailModifyScope},
		},
		AutoBootstrapDatabase: true,
	})
	applyStandardSecrets(cfg)
	standardAuthState.mu.Lock()
	standardAuthState.cfg = cfg
	standardAuthState.mu.Unlock()
	if _, ok := runtime.LookupEndpoint("auth", "GoogleConnectStart"); !ok {
		registerStandardAuthEndpoints(cfg)
	}
	svc, err := standardAuthService(ctx)
	if err != nil {
		t.Fatalf("standard auth service: %v", err)
	}
	_, rec := runFakeGoogleFlow(t, fake, "/")
	refreshCookie := findCookie(rec.Result().Cookies(), refreshCookieName)
	if refreshCookie == nil {
		t.Fatal("sign-in did not set refresh cookie")
	}
	authData := assertRefreshCookieValid(t, svc, refreshCookie.Value)
	return svc, fake, authData
}

func allowGoogleScopeForTest(scope string) {
	standardAuthState.mu.Lock()
	defer standardAuthState.mu.Unlock()
	standardAuthState.cfg.GoogleOAuth.AllowedScopes = append(standardAuthState.cfg.GoogleOAuth.AllowedScopes, scope)
}

func runFakeGoogleConnectionFlow(t *testing.T, fake *fakeGoogleServer, authData *AuthData, redirectPath string) *httptest.ResponseRecorder {
	t.Helper()
	resp, err := runtime.CallEndpoint(
		WithContext(t.Context(), UID(authData.UserID), authData),
		"auth",
		"GoogleConnectStart",
		nil,
		&GoogleConnectStartParams{Scopes: []string{gmailModifyScope}, RedirectPath: redirectPath},
	)
	if err != nil {
		t.Fatalf("GoogleConnectStart endpoint: %v", err)
	}
	start := resp.(*GoogleConnectStartResponse)
	callbackURL := fake.authorize(t, start.AuthorizeURL)
	callbackReq := httptest.NewRequest(http.MethodGet, callbackURL, nil)
	callbackRec := httptest.NewRecorder()
	GoogleCallback(callbackRec, callbackReq)
	if callbackRec.Code != http.StatusFound {
		t.Fatalf("connection callback status = %d, want %d: %s", callbackRec.Code, http.StatusFound, callbackRec.Body.String())
	}
	return callbackRec
}

func mustParseAuthUUID(t *testing.T, value string) authdb.UUID {
	t.Helper()
	id, err := parseUUID(value)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", value, err)
	}
	return id
}

type fakeGoogleServer struct {
	server        *httptest.Server
	key           *rsa.PrivateKey
	kid           string
	subject       string
	email         string
	emailVerified bool
	nonceOverride string
	accessToken   string
	refreshToken  string
	scope         string
	mu            sync.Mutex
	codes         map[string]fakeGoogleCode
	refreshQueue  []fakeGoogleRefreshResponse
	nextCode      int
	refreshCalls  atomic.Int64
	revokeCalls   atomic.Int64
}

type fakeGoogleCode struct {
	nonce string
	scope string
}

type fakeGoogleRefreshResponse struct {
	status       int
	accessToken  string
	refreshToken string
	scope        string
	errorCode    string
}

func newFakeGoogleServer(t *testing.T) *fakeGoogleServer {
	t.Helper()
	fake := &fakeGoogleServer{
		key:           mustRSAKey(t),
		kid:           "fake-google-key",
		emailVerified: true,
		codes:         map[string]fakeGoogleCode{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth", fake.handleAuth)
	mux.HandleFunc("/token", fake.handleToken)
	mux.HandleFunc("/revoke", func(w http.ResponseWriter, _ *http.Request) {
		fake.revokeCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJWKS(t, w, []testJWK{{kid: fake.kid, key: &fake.key.PublicKey}})
	})
	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)
	return fake
}

func overrideGoogleEndpointsForTest(t *testing.T, fake *fakeGoogleServer) {
	t.Helper()
	oldAuthEndpoint := googleAuthEndpoint
	oldTokenEndpoint := googleTokenEndpoint
	oldRevokeEndpoint := googleRevokeEndpoint
	oldJWKSURL := googleJWKSURL
	oldSecrets := secrets
	resetGoogleJWKSCacheForTest()
	googleAuthEndpoint = fake.server.URL + "/auth"
	googleTokenEndpoint = fake.server.URL + "/token"
	googleRevokeEndpoint = fake.server.URL + "/revoke"
	googleJWKSURL = fake.server.URL + "/jwks"
	t.Cleanup(func() {
		googleAuthEndpoint = oldAuthEndpoint
		googleTokenEndpoint = oldTokenEndpoint
		googleRevokeEndpoint = oldRevokeEndpoint
		googleJWKSURL = oldJWKSURL
		secrets = oldSecrets
		resetGoogleJWKSCacheForTest()
	})
}

func (f *fakeGoogleServer) authorize(t *testing.T, authLocation string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, authLocation, nil)
	rec := httptest.NewRecorder()
	f.server.Config.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("fake google auth status = %d, want %d", rec.Code, http.StatusFound)
	}
	return rec.Header().Get("Location")
}

func (f *fakeGoogleServer) handleAuth(w http.ResponseWriter, req *http.Request) {
	redirectURI := strings.TrimSpace(req.URL.Query().Get("redirect_uri"))
	state := strings.TrimSpace(req.URL.Query().Get("state"))
	nonce := strings.TrimSpace(req.URL.Query().Get("nonce"))
	scope := strings.TrimSpace(req.URL.Query().Get("scope"))
	if redirectURI == "" || state == "" || nonce == "" {
		http.Error(w, "bad fake auth request", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.nextCode++
	code := "code-" + strconv.Itoa(f.nextCode)
	f.codes[code] = fakeGoogleCode{nonce: nonce, scope: scope}
	f.mu.Unlock()

	callback, _ := url.Parse(redirectURI)
	query := callback.Query()
	query.Set("code", code)
	query.Set("state", state)
	callback.RawQuery = query.Encode()
	http.Redirect(w, req, callback.String(), http.StatusFound)
}

func (f *fakeGoogleServer) handleToken(w http.ResponseWriter, req *http.Request) {
	if err := req.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if req.PostForm.Get("grant_type") == "refresh_token" {
		f.handleRefresh(w)
		return
	}
	code := strings.TrimSpace(req.PostForm.Get("code"))
	f.mu.Lock()
	issued, ok := f.codes[code]
	f.mu.Unlock()
	if !ok {
		http.Error(w, "unknown code", http.StatusBadRequest)
		return
	}
	nonce := issued.nonce
	if f.nonceOverride != "" {
		nonce = f.nonceOverride
	}
	token, err := googleIDTokenWithOptions(f.key, f.kid, googleTokenOptions{
		email:         f.email,
		emailVerified: f.emailVerified,
		nonce:         nonce,
		subject:       f.subject,
	})
	if err != nil {
		http.Error(w, "sign fake token", http.StatusInternalServerError)
		return
	}
	payload := map[string]any{
		"id_token":      token,
		"access_token":  firstNonEmpty(f.accessToken, "access-"+code),
		"expires_in":    3600,
		"scope":         firstNonEmpty(f.scope, issued.scope),
		"refresh_token": f.refreshToken,
	}
	if f.refreshToken == "" {
		delete(payload, "refresh_token")
	}
	_ = json.NewEncoder(w).Encode(payload)
}

func (f *fakeGoogleServer) handleRefresh(w http.ResponseWriter) {
	call := f.refreshCalls.Add(1)
	f.mu.Lock()
	var next fakeGoogleRefreshResponse
	if len(f.refreshQueue) > 0 {
		next = f.refreshQueue[0]
		f.refreshQueue = f.refreshQueue[1:]
	}
	f.mu.Unlock()
	if next.status != 0 && (next.status < http.StatusOK || next.status >= http.StatusMultipleChoices) {
		w.WriteHeader(next.status)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":             firstNonEmpty(next.errorCode, "server_error"),
			"error_description": "scripted fake Google refresh error",
		})
		return
	}
	accessToken := firstNonEmpty(next.accessToken, "refreshed-access-"+strconv.FormatInt(call, 10))
	scope := firstNonEmpty(next.scope, f.scope)
	payload := map[string]any{
		"access_token": accessToken,
		"expires_in":   3600,
		"scope":        scope,
	}
	if next.refreshToken != "" {
		payload["refresh_token"] = next.refreshToken
	}
	_ = json.NewEncoder(w).Encode(payload)
}

type testJWK struct {
	kid string
	key *rsa.PublicKey
}

func resetGoogleJWKSCacheForTest() {
	googleJWKSCache.mu.Lock()
	defer googleJWKSCache.mu.Unlock()
	googleJWKSCache.url = ""
	googleJWKSCache.fetchedAt = time.Time{}
	googleJWKSCache.keys = nil
}

func mustRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	return key
}

func mustGoogleIDToken(t *testing.T, key *rsa.PrivateKey, kid string) string {
	t.Helper()
	token, err := googleIDTokenWithOptions(key, kid, googleTokenOptions{
		email:         "person@example.test",
		emailVerified: true,
	})
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return token
}

type googleTokenOptions struct {
	email         string
	emailVerified bool
	nonce         string
	subject       string
}

func googleIDTokenWithOptions(key *rsa.PrivateKey, kid string, opts googleTokenOptions) (string, error) {
	if opts.email == "" {
		opts.email = "person@example.test"
	}
	if opts.subject == "" {
		opts.subject = "google-subject"
	}
	claims := googleIDClaims{
		Email:         opts.email,
		EmailVerified: opts.emailVerified,
		Nonce:         opts.nonce,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "https://accounts.google.com",
			Subject:   opts.subject,
			Audience:  jwt.ClaimStrings{"client-id"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid
	out, err := token.SignedString(key)
	if err != nil {
		return "", err
	}
	return out, nil
}

func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

func writeTestJWKS(t *testing.T, w http.ResponseWriter, keys []testJWK) {
	t.Helper()
	payload := struct {
		Keys []map[string]string `json:"keys"`
	}{}
	for _, key := range keys {
		payload.Keys = append(payload.Keys, map[string]string{
			"kty": "RSA",
			"kid": key.kid,
			"alg": "RS256",
			"use": "sig",
			"n":   base64.RawURLEncoding.EncodeToString(key.key.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.key.E)).Bytes()),
		})
	}
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("write jwks: %v", err)
	}
}
