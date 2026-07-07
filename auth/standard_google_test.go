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
	"scenery.sh/runtime"
)

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

func TestRedirectGoogleCallbackErrorUsesAppSignInURL(t *testing.T) {
	oldSecrets := secrets
	t.Cleanup(func() { secrets = oldSecrets })
	secrets.PublicAppURL = "https://app.example.test"

	req := httptest.NewRequest(http.MethodGet, "/auth/google/callback", nil)
	rec := httptest.NewRecorder()
	redirectGoogleCallbackError(rec, req, "google_token")

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}
	if got, want := rec.Header().Get("Location"), "https://app.example.test/sign-in?error=google_token"; got != want {
		t.Fatalf("location = %q, want %q", got, want)
	}
}

type fakeGoogleServer struct {
	server        *httptest.Server
	key           *rsa.PrivateKey
	kid           string
	subject       string
	email         string
	emailVerified bool
	nonceOverride string
	mu            sync.Mutex
	codes         map[string]string
	nextCode      int
}

func newFakeGoogleServer(t *testing.T) *fakeGoogleServer {
	t.Helper()
	fake := &fakeGoogleServer{
		key:           mustRSAKey(t),
		kid:           "fake-google-key",
		emailVerified: true,
		codes:         map[string]string{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth", fake.handleAuth)
	mux.HandleFunc("/token", fake.handleToken)
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
	oldJWKSURL := googleJWKSURL
	oldSecrets := secrets
	resetGoogleJWKSCacheForTest()
	googleAuthEndpoint = fake.server.URL + "/auth"
	googleTokenEndpoint = fake.server.URL + "/token"
	googleJWKSURL = fake.server.URL + "/jwks"
	t.Cleanup(func() {
		googleAuthEndpoint = oldAuthEndpoint
		googleTokenEndpoint = oldTokenEndpoint
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
	if redirectURI == "" || state == "" || nonce == "" {
		http.Error(w, "bad fake auth request", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.nextCode++
	code := "code-" + strconv.Itoa(f.nextCode)
	f.codes[code] = nonce
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
	code := strings.TrimSpace(req.PostForm.Get("code"))
	f.mu.Lock()
	nonce, ok := f.codes[code]
	f.mu.Unlock()
	if !ok {
		http.Error(w, "unknown code", http.StatusBadRequest)
		return
	}
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
	_ = json.NewEncoder(w).Encode(map[string]string{"id_token": token})
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
