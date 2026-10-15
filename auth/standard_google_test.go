package auth

import (
	"crypto/rsa"
	"crypto/x509"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	authdb "scenery.sh/auth/db/gen"
)

// Public HTTP journeys and PostgreSQL row-lock single-flight proof live in the
// mandatory standard-auth release probe. These roots retain in-process decisions.
func TestGoogleOAuthBrowserFlowWithFakeGoogle(t *testing.T) {
	var saved []driver.NamedValue
	svc, _ := authSQLService(t, authSQLStep{name: "DeleteExpiredOAuthStates"}, authSQLStep{name: "CreateOAuthState", rows: [][]driver.Value{googleLoginStateRow("")}, check: func(_ string, args []driver.NamedValue) { saved = append([]driver.NamedValue(nil), args...) }})
	setupGoogleSignInTest(t, svc)
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

type googleTestTransport func(*http.Request) *http.Response

func (f googleTestTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req), nil }
func googleTestHTTP(t *testing.T, handler func(*http.Request) *http.Response) {
	t.Helper()
	old := googleHTTPClient
	t.Cleanup(func() { googleHTTPClient = old })
	googleHTTPClient = &http.Client{Transport: googleTestTransport(handler)}
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

// setConfiguredAuthSecretForTest stands in for one auth.* secret of the
// process's configuration snapshot.
func setConfiguredAuthSecretForTest(t *testing.T, key, value string) {
	t.Helper()
	previous := configuredAuthSecret
	configuredAuthSecret = func(requested string) ([]byte, bool, error) {
		if requested == key {
			return []byte(value), true, nil
		}
		return previous(requested)
	}
	t.Cleanup(func() { configuredAuthSecret = previous })
}

func setupGoogleSignInTest(t *testing.T, svc *Service) {
	t.Helper()
	installCurrentUserTestService(t, svc)
	old := secrets
	t.Cleanup(func() { secrets = old })
	secrets.GoogleOAuthClientID = "client-id"
	secrets.GoogleOAuthClientSecret = "client-secret"
	secrets.APIBaseURL = "https://api.example.test"
	secrets.PublicAppURL = "https://app.example.test"
	setConfiguredAuthSecretForTest(t, "auth.token_cipher_key", base64.StdEncoding.EncodeToString([]byte("12345678901234567890123456789012")))
	standardAuthState.mu.Lock()
	standardAuthState.cfg = normalizeStandardConfig(StandardConfig{Enabled: true, GoogleOAuth: GoogleOAuthConfig{Enabled: true}})
	standardAuthState.mu.Unlock()
}

func googleLoginStateRow(purpose string) []driver.Value {
	return []driver.Value{"11111111-1111-1111-1111-111111111111", "hash", "verifier", "nonce-hash", authSQLUserID, purpose, "/settings", authSQLNow.Add(time.Hour), nil, authSQLNow}
}
