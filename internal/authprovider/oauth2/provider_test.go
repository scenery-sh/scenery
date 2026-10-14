package oauth2_test

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/authprovider/google"
	"scenery.sh/internal/authprovider/oauth2"
	"scenery.sh/internal/connectionvault"
)

// This is a fixed public test-only 2048-bit RSA key, not generated
// during a test root and never a deployment credential.
//
//go:embed testdata/signing-key.der
var signingDER []byte

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

var now = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

func oauthConfig() oauth2.Config {
	return oauth2.Config{Issuer: "https://identity.example", AuthorizeURL: "https://identity.example/authorize", TokenURL: "https://identity.example/token", AccountURL: "https://identity.example/account", ClientID: "client", ClientSecret: "secret", Resource: "https://api.example", SubjectField: "id"}
}

func TestOAuth2OnlyProviderUsesPKCEAndConfiguredAccountIdentity(t *testing.T) {
	cfg := oauthConfig()
	calls := 0
	client := &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		switch req.URL.Path {
		case "/token":
			body, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatal(err)
			}
			form, err := url.ParseQuery(string(body))
			if err != nil {
				t.Fatal(err)
			}
			id, secret, ok := req.BasicAuth()
			if !ok || id != "client" || secret != "secret" || form.Get("grant_type") != "authorization_code" || form.Get("code_verifier") != "verifier" || form.Get("resource") != cfg.Resource {
				t.Fatalf("token binding: %v", form)
			}
			return response(200, `{"access_token":"opaque-access","token_type":"Bearer","expires_in":3600}`), nil
		case "/account":
			if req.Header.Get("Authorization") != "Bearer opaque-access" {
				t.Fatal("account request is not bound to the exchanged bearer")
			}
			return response(200, `{"id":"account-subject","email":"account@example.test"}`), nil
		default:
			t.Fatalf("unexpected request: %s", req.URL)
			return nil, errors.New("unexpected")
		}
	})}
	p := oauth2.New(cfg, client, func() time.Time { return now })
	authorize, err := p.Authorize(connectionvault.Authorization{State: "state", Verifier: "verifier", CallbackURI: "https://app.example/auth/connections/test/callback", Scopes: []string{"Read"}})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authorize)
	digest := sha256.Sum256([]byte("verifier"))
	if u.Query().Get("code_challenge_method") != "S256" || u.Query().Get("code_challenge") != base64.RawURLEncoding.EncodeToString(digest[:]) || u.Query().Get("resource") != cfg.Resource || u.Query().Get("scope") != "Read" || u.Query().Has("nonce") {
		t.Fatalf("authorization: %s", authorize)
	}
	grant, err := p.Exchange(t.Context(), connectionvault.CodeExchange{Code: "code", Verifier: "verifier", CallbackURI: "https://app.example/auth/connections/test/callback", Scopes: []string{"Read"}})
	if err != nil || grant.Subject != "account-subject" || grant.Issuer != cfg.Issuer || grant.Resource != cfg.Resource || grant.RefreshToken != "" || !grant.ScopesPresent || !slices.Equal(grant.Scopes, []string{"Read"}) || calls != 2 {
		t.Fatalf("OAuth2 account grant: %#v, %v, calls=%d", grant, err, calls)
	}
}

func TestProviderRefreshPreservesOptionalFieldsAndRejectsWrongResource(t *testing.T) {
	for _, scenario := range []string{"omitted", "empty_scope", "wrong_resource", "no_expiry"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := oauthConfig()
			body := `{"access_token":"access","token_type":"Bearer","expires_in":3600}`
			switch scenario {
			case "empty_scope":
				body = `{"access_token":"access","token_type":"Bearer","scope":""}`
			case "wrong_resource":
				body = `{"access_token":"access","token_type":"Bearer","resource":"other"}`
			case "no_expiry":
				body = `{"access_token":"access","token_type":"Bearer"}`
			}
			p := oauth2.New(cfg, &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { return response(200, body), nil })}, func() time.Time { return now })
			grant, err := p.Refresh(t.Context(), "refresh")
			if scenario == "wrong_resource" {
				if err == nil {
					t.Fatal("wrong resource accepted")
				}
				return
			}
			if err != nil || grant.RefreshToken != "" {
				t.Fatalf("optional token fields: %#v, %v", grant, err)
			}
			if scenario == "empty_scope" && !grant.ScopesPresent || scenario == "omitted" && grant.ScopesPresent {
				t.Fatal("missing and empty scopes were conflated")
			}
			if scenario == "no_expiry" && !grant.ExpiresAt.IsZero() {
				t.Fatal("expiry was invented")
			}
		})
	}
}

func TestProviderFailuresAreSanitizedAndAmbiguousRefreshIsNotRetried(t *testing.T) {
	for _, scenario := range []struct {
		body    string
		kind    connectionvault.FailureKind
		network bool
	}{{`{"error":"invalid_grant","error_description":"secret-credential"}`, connectionvault.Revoked, false}, {`{"error":"invalid_client","error_description":"secret-credential"}`, connectionvault.Misconfigured, false}, {`{"error":"temporarily_unavailable"}`, connectionvault.Temporary, false}, {`not-json secret-credential`, connectionvault.UnknownOutcome, false}, {"", connectionvault.UnknownOutcome, true}} {
		t.Run(string(scenario.kind)+scenario.body, func(t *testing.T) {
			calls := 0
			p := oauth2.New(oauthConfig(), &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				calls++
				if scenario.network {
					return nil, errors.New("lost response secret-credential")
				}
				return response(500, scenario.body), nil
			})}, nil)
			_, err := p.Refresh(t.Context(), "refresh")
			var classified connectionvault.ProviderError
			if !errors.As(err, &classified) || classified.Kind != scenario.kind || strings.Contains(err.Error(), "secret-credential") || calls != 1 {
				t.Fatalf("classification/retry: %v, calls=%d", err, calls)
			}
		})
	}
}

func fixtureKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := x509.ParsePKCS1PrivateKey(signingDER)
	if err != nil {
		t.Fatal(err)
	}
	return key
}
func signedIdentity(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	encode := func(value any) string {
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	unsigned := encode(map[string]any{"alg": "RS256", "kid": "key"}) + "." + encode(claims)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
}
func jwks(t *testing.T, key *rsa.PrivateKey) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"keys": []any{map[string]any{"kid": "key", "kty": "RSA", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestGooglePresetUsesSharedOIDCProviderAndChecksIdentityBindings(t *testing.T) {
	key := fixtureKey(t)
	for _, scenario := range []string{"valid", "issuer", "audience", "expiry", "nonce", "signature"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := google.Config("client", "secret", "")
			claims := map[string]any{"iss": cfg.Issuer, "sub": "provider-account", "aud": "client", "exp": now.Add(time.Hour).Unix(), "iat": now.Unix(), "nonce": "nonce", "email": "person@example.test"}
			switch scenario {
			case "issuer":
				claims["iss"] = "https://wrong.example"
			case "audience":
				claims["aud"] = "other"
			case "expiry":
				claims["exp"] = now.Add(-time.Second).Unix()
			case "nonce":
				claims["nonce"] = "wrong"
			}
			token := signedIdentity(t, key, claims)
			if scenario == "signature" {
				parts := strings.Split(token, ".")
				signature, _ := base64.RawURLEncoding.DecodeString(parts[2])
				signature[0] ^= 1
				parts[2] = base64.RawURLEncoding.EncodeToString(signature)
				token = strings.Join(parts, ".")
			}
			client := &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.String() == cfg.JWKSURL {
					return response(200, jwks(t, key)), nil
				}
				if req.URL.String() != cfg.TokenURL {
					t.Fatalf("unexpected provider request: %s", req.URL)
				}
				body, err := json.Marshal(map[string]any{"access_token": "access", "refresh_token": "refresh", "token_type": "Bearer", "id_token": token, "expires_in": 3600})
				if err != nil {
					t.Fatal(err)
				}
				return response(200, string(body)), nil
			})}
			p := oauth2.New(cfg, client, func() time.Time { return now })
			authorize, err := p.Authorize(connectionvault.Authorization{State: "state", Verifier: "verifier", Nonce: "nonce", CallbackURI: "https://app.example/auth/connections/google/callback", Consent: true, Scopes: []string{"https://www.googleapis.com/auth/gmail.modify"}})
			if err != nil {
				t.Fatal(err)
			}
			u, _ := url.Parse(authorize)
			if u.Query().Get("access_type") != "offline" || u.Query().Get("include_granted_scopes") != "true" || u.Query().Get("prompt") != "consent" || !strings.Contains(u.Query().Get("scope"), "openid") {
				t.Fatalf("Google consent: %s", authorize)
			}
			digest := sha256.Sum256([]byte("nonce"))
			grant, err := p.Exchange(t.Context(), connectionvault.CodeExchange{Code: "code", Verifier: "verifier", NonceHash: hex.EncodeToString(digest[:]), CallbackURI: "https://app.example/auth/connections/google/callback", Scopes: []string{"https://www.googleapis.com/auth/gmail.modify"}})
			if scenario != "valid" {
				if err == nil {
					t.Fatalf("invalid %s identity accepted", scenario)
				}
				return
			}
			if err != nil || grant.Subject != "provider-account" || grant.Issuer != cfg.Issuer {
				t.Fatalf("OIDC grant: %#v, %v", grant, err)
			}
		})
	}
}

func TestProviderConstructionAndInvalidEndpointsDoNotInitializeNetwork(t *testing.T) {
	calls := 0
	cfg := oauthConfig()
	cfg.TokenURL = "http://untrusted.example/token"
	p := oauth2.New(cfg, &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected") })}, nil)
	if calls != 0 {
		t.Fatal("construction made a provider request")
	}
	_, err := p.Refresh(t.Context(), "refresh")
	var classified connectionvault.ProviderError
	if !errors.As(err, &classified) || classified.Kind != connectionvault.Misconfigured || calls != 0 {
		t.Fatalf("untrusted endpoint: %v", err)
	}
}
