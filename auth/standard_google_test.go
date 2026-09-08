package auth

import (
	"crypto/rsa"
	"crypto/x509"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	authdb "scenery.sh/auth/db/gen"
	"scenery.sh/errs"
)

const gmailModifyScope = "https://www.googleapis.com/auth/gmail.modify"
const calendarEventsScope = "https://www.googleapis.com/auth/calendar.events"

// Public HTTP journeys and PostgreSQL row-lock single-flight proof live in the
// mandatory standard-auth release probe. These roots retain in-process decisions.
func TestGoogleOAuthBrowserFlowWithFakeGoogle(t *testing.T) {
	var saved []driver.NamedValue
	svc, _ := authSQLService(t, authSQLStep{name: "DeleteExpiredOAuthStates"}, authSQLStep{name: "CreateOAuthState", rows: [][]driver.Value{googleOAuthStateRow("")}, check: func(_ string, args []driver.NamedValue) { saved = append([]driver.NamedValue(nil), args...) }})
	setupGoogleDecisionTest(t, svc)
	rec := httptest.NewRecorder()
	GoogleStart(rec, httptest.NewRequest(http.MethodGet, "https://app.example.test/auth/google/start?redirect_path=/welcome", nil))
	location, err := url.Parse(rec.Header().Get("Location"))
	if err != nil || rec.Code != http.StatusFound || len(saved) != 6 {
		t.Fatalf("start status=%d location=%v args=%v", rec.Code, location, saved)
	}
	q := location.Query()
	if q.Get("state") == "" || q.Get("nonce") == "" || saved[1].Value != tokenHash(q.Get("state")) || saved[3].Value != tokenHash(q.Get("nonce")) {
		t.Fatal("state and nonce must be stored as hashes")
	}
	verifier := saved[2].Value.(string)
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") != googlePKCEChallenge(verifier) || saved[4].Value != "/welcome" || saved[5].Value != authSQLNow.Add(defaultOAuthStateTTL) {
		t.Fatalf("PKCE or redirect/expiry contract: %v %v", q, saved)
	}
}

func TestGoogleConnectionStartFallsBackToConfiguredAPIBaseURL(t *testing.T) {
	old := secrets
	t.Cleanup(func() { secrets = old })
	secrets.APIBaseURL = "https://api.example.test"
	if got := googleConnectionRedirectURI(nil); got != "https://api.example.test/auth/google/callback" {
		t.Fatalf("callback=%q", got)
	}
	state, verifier, nonce, err := newGoogleOAuthMaterial()
	if err != nil || len(state) < 32 || len(verifier) < 43 || len(nonce) < 24 || state == verifier || state == nonce {
		t.Fatalf("OAuth material lengths=%d/%d/%d err=%v", len(state), len(verifier), len(nonce), err)
	}
}

func TestGoogleConnectionFlowStoresEncryptedTokenAndDisconnects(t *testing.T) {
	svc, _ := authSQLService(t)
	setupGoogleDecisionTest(t, svc)
	cipher, err := sealGoogleToken("refresh-secret")
	if err != nil || strings.Contains(string(cipher), "refresh-secret") {
		t.Fatalf("sealed token=%v", err)
	}
	conn := authdb.SceneryAuthGoogleConnection{Status: "active", Scopes: gmailModifyScope, RefreshTokenCiphertext: cipher}
	if !googleConnectionHasUsableRefresh(conn, []string{gmailModifyScope}) || googleConnectionHasUsableRefresh(conn, []string{calendarEventsScope}) {
		t.Fatal("usable refresh scope contract")
	}
	conn.Status = "disconnected"
	if googleConnectionHasUsableRefresh(conn, []string{gmailModifyScope}) || disconnectedGoogleConnectionResponse().Status != "disconnected" {
		t.Fatal("disconnected refresh must not be usable")
	}
	calls := 0
	googleTestHTTP(t, func(req *http.Request) *http.Response {
		calls++
		if err := req.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if req.URL.String() != googleRevokeEndpoint || req.Method != http.MethodPost || req.Form.Get("token") != "refresh-secret" {
			t.Fatalf("revoke request=%s %s %v", req.Method, req.URL, req.Form)
		}
		return googleTestResponse(200, "{}")
	})
	if err := revokeGoogleToken(t.Context(), "refresh-secret"); err != nil || calls != 1 {
		t.Fatalf("revoke=%v calls=%d", err, calls)
	}
}

func TestGoogleConnectionCallbackOAuthErrorUsesStateRedirect(t *testing.T) {
	svc, _ := authSQLService(t, authSQLStep{name: "ConsumeOAuthState", rows: [][]driver.Value{googleOAuthStateRow(googleConnectionOAuthPurpose)}, check: func(_ string, args []driver.NamedValue) {
		if args[0].Value != tokenHash("state-secret") {
			t.Fatalf("state lookup=%v", args)
		}
	}})
	setupGoogleDecisionTest(t, svc)
	rec := httptest.NewRecorder()
	GoogleCallback(rec, httptest.NewRequest(http.MethodGet, "https://app.example.test/auth/google/callback?error=access_denied&state=state-secret", nil))
	if got := rec.Header().Get("Location"); rec.Code != http.StatusFound || got != "https://app.example.test/settings?error=google_oauth" {
		t.Fatalf("callback=%d %q", rec.Code, got)
	}
}

func TestGoogleAccessTokenRefreshesRotatesAndSingleFlights(t *testing.T) {
	svc, script := authSQLService(t)
	setupGoogleDecisionTest(t, svc)
	row := googleConnectionRow(t, "active")
	script.steps = []authSQLStep{
		googleLockedConnectionStep(t, row),
		{name: "UpdateGoogleConnectionTokens", rows: [][]driver.Value{row}, check: func(_ string, args []driver.NamedValue) {
			for i, want := range map[int]string{2: "rotated-refresh", 3: "rotated-access"} {
				cipher, ok := args[i].Value.([]byte)
				if !ok {
					t.Fatalf("token is not ciphertext: %T", args[i].Value)
				}
				got, err := openGoogleToken(cipher)
				if err != nil || got != want || strings.Contains(string(cipher), want) {
					t.Fatalf("persisted token=%q err=%v", got, err)
				}
			}
			if args[1].Value != gmailModifyScope || args[4].Value != authSQLNow.Add(time.Hour) {
				t.Fatalf("rotation scope/expiry=%v", args)
			}
		}},
	}
	calls := 0
	googleTestHTTP(t, func(req *http.Request) *http.Response {
		calls++
		if err := req.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if req.Form.Get("refresh_token") != "initial-refresh" || req.Form.Get("grant_type") != "refresh_token" {
			t.Fatalf("refresh form=%v", req.Form)
		}
		return googleTestResponse(200, `{"access_token":"rotated-access","refresh_token":"rotated-refresh","expires_in":3600,"scope":"`+gmailModifyScope+`"}`)
	})
	token, err := GoogleAccessTokenForUser(t.Context(), authSQLUserID, gmailModifyScope)
	if err != nil || token != "rotated-access" || calls != 1 || !reflect.DeepEqual(script.events, []string{"begin", "GetGoogleConnectionByUserForUpdate", "UpdateGoogleConnectionTokens", "commit"}) {
		t.Fatalf("refresh=%q err=%v calls=%d events=%v", token, err, calls, script.events)
	}
	row[6], err = sealGoogleToken("cached-access")
	if err != nil {
		t.Fatal(err)
	}
	row[7] = authSQLNow.Add(time.Hour)
	script.steps = []authSQLStep{googleLockedConnectionStep(t, row)}
	token, err = GoogleAccessTokenForUser(t.Context(), authSQLUserID, gmailModifyScope)
	if err != nil || token != "cached-access" || calls != 1 {
		t.Fatalf("cache=%q err=%v calls=%d", token, err, calls)
	}
}

func TestGoogleAccessTokenRetriesTransientAndMarksPermanentRefreshFailures(t *testing.T) {
	svc, script := authSQLService(t)
	setupGoogleDecisionTest(t, svc)
	oldBackoff := googleRefreshRetryBackoff
	googleRefreshRetryBackoff = 0
	t.Cleanup(func() { googleRefreshRetryBackoff = oldBackoff })
	calls := 0
	googleTestHTTP(t, func(_ *http.Request) *http.Response {
		calls++
		switch calls {
		case 1:
			return googleTestResponse(500, `{"error":"server_error"}`)
		case 2:
			return googleTestResponse(200, `{"access_token":"retried-access"}`)
		default:
			return googleTestResponse(400, `{"error":"invalid_grant"}`)
		}
	})
	token, err := refreshGoogleAccessToken(t.Context(), "initial-refresh")
	if err != nil || token.AccessToken != "retried-access" || calls != 2 {
		t.Fatalf("retry=%+v err=%v calls=%d", token, err, calls)
	}
	row := googleConnectionRow(t, "active")
	script.steps = []authSQLStep{googleLockedConnectionStep(t, row), {name: "MarkGoogleConnectionReauthRequired", rows: [][]driver.Value{row}, check: func(_ string, args []driver.NamedValue) {
		if args[1].Value != "invalid_grant" {
			t.Fatalf("reauth reason=%v", args)
		}
	}}}
	_, err = GoogleAccessTokenForUser(t.Context(), authSQLUserID, gmailModifyScope)
	if errs.Code(err) != errs.GoogleReauthRequired || calls != 3 || script.events[len(script.events)-1] != "commit" {
		t.Fatalf("permanent=%v calls=%d events=%v", err, calls, script.events)
	}
	row[8] = "reauth_required"
	script.steps = []authSQLStep{googleLockedConnectionStep(t, row)}
	_, err = GoogleAccessTokenForUser(t.Context(), authSQLUserID, gmailModifyScope)
	if errs.Code(err) != errs.GoogleReauthRequired || calls != 3 {
		t.Fatalf("reauth=%v calls=%d", err, calls)
	}
}

func TestGoogleAccessTokenReportsMissingScopes(t *testing.T) {
	svc, script := authSQLService(t)
	setupGoogleDecisionTest(t, svc)
	script.steps = []authSQLStep{googleLockedConnectionStep(t, googleConnectionRow(t, "active"))}
	_, err := svc.googleAccessTokenForUser(t.Context(), mustParseAuthUUID(t, authSQLUserID), []string{calendarEventsScope})
	if errs.Code(err) != errs.GoogleScopeMissing || script.events[len(script.events)-1] != "rollback" {
		t.Fatalf("missing scope=%v events=%v", err, script.events)
	}
}

func TestGoogleAccessTokenEnforcesAllowedScopes(t *testing.T) {
	svc, script := authSQLService(t)
	setupGoogleDecisionTest(t, svc)
	_, err := GoogleAccessTokenForUser(t.Context(), authSQLUserID, calendarEventsScope)
	if errs.Code(err) != errs.PermissionDenied || len(script.events) != 0 {
		t.Fatalf("disallowed scope=%v events=%v", err, script.events)
	}
}

func setupGoogleDecisionTest(t *testing.T, svc *Service) {
	t.Helper()
	installCurrentUserTestService(t, svc)
	old := secrets
	t.Cleanup(func() { secrets = old })
	secrets.GoogleOAuthClientID = "client-id"
	secrets.GoogleOAuthClientSecret = "client-secret"
	secrets.APIBaseURL = "https://api.example.test"
	secrets.PublicAppURL = "https://app.example.test"
	t.Setenv("AUTH_TOKEN_CIPHER_KEY", base64.StdEncoding.EncodeToString([]byte("12345678901234567890123456789012")))
	standardAuthState.mu.Lock()
	standardAuthState.cfg = normalizeStandardConfig(StandardConfig{Enabled: true, GoogleOAuth: GoogleOAuthConfig{Enabled: true, AllowedScopes: []string{gmailModifyScope}}})
	standardAuthState.mu.Unlock()
}

type googleTestTransport func(*http.Request) *http.Response

func (f googleTestTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req), nil }
func googleTestHTTP(t *testing.T, handler func(*http.Request) *http.Response) {
	t.Helper()
	old := googleHTTPClient
	t.Cleanup(func() { googleHTTPClient = old })
	googleHTTPClient = &http.Client{Transport: googleTestTransport(handler)}
}
func googleTestResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func googleOAuthStateRow(purpose string) []driver.Value {
	return []driver.Value{"11111111-1111-1111-1111-111111111111", "hash", "verifier", "nonce-hash", authSQLUserID, purpose, "/settings", authSQLNow.Add(time.Hour), nil, authSQLNow}
}
func googleConnectionRow(t *testing.T, status string) []driver.Value {
	t.Helper()
	refresh, err := sealGoogleToken("initial-refresh")
	if err != nil {
		t.Fatal(err)
	}
	return []driver.Value{"11111111-1111-1111-1111-111111111111", authSQLUserID, "google-subject", "person@example.test", gmailModifyScope, refresh, []byte{}, authSQLNow.Add(-time.Minute), status, nil, "", authSQLNow, nil, authSQLNow, authSQLNow}
}
func googleLockedConnectionStep(t *testing.T, row []driver.Value) authSQLStep {
	t.Helper()
	return authSQLStep{name: "GetGoogleConnectionByUserForUpdate", rows: [][]driver.Value{row}, check: func(query string, _ []driver.NamedValue) {
		if !strings.Contains(query, "FOR UPDATE") {
			t.Fatal("refresh must select under a row lock")
		}
	}}
}

type testJWK struct {
	kid string
	key *rsa.PublicKey
}
type googleTokenOptions struct {
	email         string
	emailVerified bool
	nonce         string
	subject       string
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
	firstKey := mustGoogleJWKSFixtureKey(t, 0)
	secondKey := mustGoogleJWKSFixtureKey(t, 1)
	firstToken := mustGoogleIDToken(t, firstKey, "kid-1")
	secondToken := mustGoogleIDToken(t, secondKey, "kid-2")
	keys := []testJWK{{kid: "kid-1", key: &firstKey.PublicKey}}
	var requests atomic.Int64
	googleTestHTTP(t, func(req *http.Request) *http.Response {
		requests.Add(1)
		rec := httptest.NewRecorder()
		writeTestJWKS(t, rec, keys)
		return rec.Result()
	})
	googleJWKSURL = "https://jwks.example.test/keys"

	if _, err := verifyGoogleIDToken(t.Context(), firstToken); err != nil {
		t.Fatalf("verify first token: %v", err)
	}
	keys = []testJWK{{kid: "kid-2", key: &secondKey.PublicKey}}
	if _, err := verifyGoogleIDToken(t.Context(), firstToken); err != nil {
		t.Fatalf("verify cached token: %v", err)
	}
	if _, err := verifyGoogleIDToken(t.Context(), secondToken); err != nil {
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

func TestGoogleConnectionResponseNormalizesContractDateTimesToUTC(t *testing.T) {
	location := time.FixedZone("local", 2*60*60)
	response := googleConnectionResponse(authdb.SceneryAuthGoogleConnection{
		Status:      "active",
		ConnectedAt: time.Date(2026, 7, 20, 13, 18, 45, 171981000, location),
	})

	if response.ConnectedAt == nil {
		t.Fatal("ConnectedAt is nil")
	}
	if got, want := response.ConnectedAt.Format(time.RFC3339Nano), "2026-07-20T11:18:45.171981Z"; got != want {
		t.Fatalf("ConnectedAt = %q, want %q", got, want)
	}
}

func TestGoogleTokenCipherRoundTrip(t *testing.T) {
	resetStandardAuthStateForTest(t)
	key := base64.StdEncoding.EncodeToString([]byte("12345678901234567890123456789012"))
	t.Setenv("AUTH_TOKEN_CIPHER_KEY", key)
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

func mustParseAuthUUID(t *testing.T, value string) authdb.UUID {
	t.Helper()
	id, err := parseUUID(value)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", value, err)
	}
	return id
}

func resetGoogleJWKSCacheForTest() {
	googleJWKSCache.mu.Lock()
	defer googleJWKSCache.mu.Unlock()
	googleJWKSCache.url = ""
	googleJWKSCache.fetchedAt = time.Time{}
	googleJWKSCache.keys = nil
}

func mustGoogleJWKSFixtureKey(t *testing.T, index int) *rsa.PrivateKey {
	t.Helper()
	der, err := base64.StdEncoding.DecodeString(googleJWKSFixtureKeys[index])
	if err != nil {
		t.Fatalf("decode google JWKS fixture %d: %v", index, err)
	}
	key, err := x509.ParsePKCS1PrivateKey(der)
	if err != nil {
		t.Fatalf("parse google JWKS fixture %d: %v", index, err)
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
		Issuer:        "https://accounts.google.com",
		Subject:       opts.subject,
		Audience:      jwt.ClaimStrings{"client-id"},
		ExpiresAt:     jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid
	out, err := token.SignedString(key)
	if err != nil {
		return "", err
	}
	return out, nil
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

var googleJWKSFixtureKeys = [2]string{
	"MIICXAIBAAKBgQDHp9/U22x1AsX1u5oH1JUg2VxwDwq2Osdp4CGiEN1Ux4J7ObrdJ0A0AwhPDqTo4wICAUNoM5dzgHykFTYZDuSWm/Ii5DBN68AHhZqIK5s/V0TucIF+/5eQCN34WahUjAS2JXboiPCZvXknwIMKHJXfqPjbeh48LIBdmbiLMn1DOwIDAQABAoGBAKNTpTuPtI2UEzUOntbBBK22onPZGj4wn2jxPRJDEYyFGSyM8Vxw+4iQ4n8pz6Xj7oSNXAMmEUMfXNctsu+Uy1Em50AqVM/mEAMxPtxmYvp7B1wnU/NdTupi1i+YNdw5qXoXIYODbi2tN4zOjIN0r2bnhQ2VM2o7po6hEzJO+K0hAkEA8N4h9TzJNfPLxAfkVdUvW+r14MWcG0JXhHF1lk0ByiYco36AzrUS4qkGLqTBK8sPv5nL42KwkAdXJ90vo1ietQJBANQy7mrx8QhjMgczzLQaXdgB3ZkmW2h+0o1SY6MvXyLynipNY3HL5mCzFtNRgcIKV2T88BnU5N7077efBedLoC8CQHBjBS87PJs69QGzuPu/rAhUeoN1UOB7NQCsO/R0W/hpjgVPOmS4omY1/Zd38lYvulppNXQUkVOyyRzlnJu39t0CQFyJqXdx8w8ZUyPY7xhLt0kP5zd2hr5XMDL5DwKHEhIHg/omrYtexCS/dODK1q9sGxirRXm+YeDpJ/EHpGdtj3kCQGvi/JY2s/Pm5h00jdxVcFyKMAsBertyyufiijwouhWB+zU0cK+VeySrQ1GDok1EjvyGKXSD9paFAUtHHaOQFpg=",
	"MIICXAIBAAKBgQDEncX1+qSsfdRezjC8jP3/ZcOiWDdr60je2461RZViT7yJ4L720LZO5xFj0g7CMO0XWSQfnk3ORs0BZcKx45MDs9yb622M+CZdUto+cwleESPpvGK0qK9k9wrayTILfTg/gDr2oWPyFiztkg0mxq8q3dQJJxE3cUBUmdT8NFrNmQIDAQABAoGAQ6eikbSwa2ZU6FZ88LR3RiWnPrqqP2lTxtO39GpAL/cOAkeijl1dDiN2mWmTiIC7ZJhY1MRtM3irXDq+1uVfFYCxnRN9A41qmauh57NFJLR1JnrMU6jUDhAAbq1koXwCqYr356XAU+EZmxlURjYJ0DJEib7Ee4N+GVCAakZh+j0CQQD7IsB/lW9+7+ZcuzKDgAC6YRLy/ERlrrd8/fTBAu4QM2HcvKkts2eQmeS9HUSaBUHQWJUFghrzaVTVrGvyJplbAkEAyGywQncILvrgdvXhXRAaMjKieiGJqnUDwG1tvH8XbLjhV/aPI4WRAqJkPTxD6Xtl+yPt4NmLIP7wEiZrpoqzGwJBAPDklOHM5fZNCBtLNVkOH6SoGRUbBkDDJx6uO2go91Jy9xxVm7JKtLzv4YnF2VgkUs0XK1rtQgzarJWJnsHYZKECQG3MVVdkHGCYYeXp19eC3ccIREiCHQf76N0/VbHBMlUGh7UHxuzf3ExEKIP/gvji+EB4M3ZN11FxOJXI5IqtS2cCQC2NK/4Yv3YVOe2GEkgIYxNwRoQNGhQy5D9mbp6MvpcJKPVOTB08C0HujWXGCtye22ZAuyBO/y97lhjF6zSPt70=",
}
