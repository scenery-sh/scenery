package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type fakeGoogle struct {
	server                                                *httptest.Server
	key                                                   *rsa.PrivateKey
	mu                                                    sync.Mutex
	email, subject, nonceOverride, access, refresh, scope string
	verified                                              bool
	codes                                                 map[string]fakeCode
	queue                                                 []fakeRefresh
	refreshInputs                                         []string
	refreshCalls, revokeCalls                             atomic.Int64
}

type fakeCode struct{ nonce, scope, challenge string }
type fakeRefresh struct {
	status                            int
	access, refresh, scope, errorCode string
}
type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newFakeGoogle() *fakeGoogle {
	f := &fakeGoogle{key: take(rsa.GenerateKey(rand.Reader, 2048)), email: "person@example.test", subject: "google-subject", verified: true, codes: map[string]fakeCode{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth", f.authorization)
	mux.HandleFunc("/token", f.token)
	mux.HandleFunc("/revoke", func(w http.ResponseWriter, _ *http.Request) { f.revokeCalls.Add(1); w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "owned-google-key", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(f.key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(f.key.E)).Bytes())}}})
	})
	f.server = httptest.NewServer(mux)
	return f
}

// Route only the four authored Google endpoints to the owned loopback server.
// Any unexpected destination fails closed; this fixture never contacts Google.
func (f *fakeGoogle) transport(base http.RoundTripper) http.RoundTripper {
	return roundTrip(func(request *http.Request) (*http.Response, error) {
		paths := map[string]string{
			"accounts.google.com/o/oauth2/v2/auth": "/auth",
			"oauth2.googleapis.com/token":          "/token",
			"oauth2.googleapis.com/revoke":         "/revoke",
			"www.googleapis.com/oauth2/v3/certs":   "/jwks",
		}
		path, ok := paths[request.URL.Host+request.URL.Path]
		if !ok || request.URL.Scheme != "https" {
			return nil, fmt.Errorf("auth fixture rejected outbound endpoint %s://%s%s", request.URL.Scheme, request.URL.Host, request.URL.Path)
		}
		clone := request.Clone(request.Context())
		target := take(url.Parse(f.server.URL))
		target.Path, target.RawQuery = path, request.URL.RawQuery
		clone.URL, clone.Host = target, target.Host
		return base.RoundTrip(clone)
	})
}

func (f *fakeGoogle) set(update func(*fakeGoogle)) { f.mu.Lock(); defer f.mu.Unlock(); update(f) }

func (f *fakeGoogle) authorize(location string) string {
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response := take(client.Get(location))
	_, err := io.Copy(io.Discard, response.Body)
	must(err)
	must(response.Body.Close())
	if response.StatusCode != http.StatusFound {
		panic(failure{fmt.Errorf("fake authorization returned %d", response.StatusCode)})
	}
	return response.Header.Get("Location")
}

func (f *fakeGoogle) authorization(w http.ResponseWriter, request *http.Request) {
	q := request.URL.Query()
	if q.Get("redirect_uri") == "" || q.Get("state") == "" || q.Get("nonce") == "" || q.Get("code_challenge_method") != "S256" || q.Get("client_id") != "client-id" {
		http.Error(w, "incomplete authorization request", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	code := "code-" + strconv.Itoa(len(f.codes)+1)
	f.codes[code] = fakeCode{nonce: q.Get("nonce"), scope: q.Get("scope"), challenge: q.Get("code_challenge")}
	f.mu.Unlock()
	callback, err := url.Parse(q.Get("redirect_uri"))
	if err != nil {
		http.Error(w, "invalid redirect", http.StatusBadRequest)
		return
	}
	query := callback.Query()
	query.Set("code", code)
	query.Set("state", q.Get("state"))
	callback.RawQuery = query.Encode()
	http.Redirect(w, request, callback.String(), http.StatusFound)
}

func (f *fakeGoogle) token(w http.ResponseWriter, request *http.Request) {
	if err := request.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	if request.PostForm.Get("client_id") != "client-id" || request.PostForm.Get("client_secret") != "client-secret" {
		http.Error(w, "invalid client", http.StatusUnauthorized)
		return
	}
	if request.PostForm.Get("grant_type") == "refresh_token" {
		f.refreshToken(w, request.PostForm.Get("refresh_token"))
		return
	}
	f.mu.Lock()
	issued, ok := f.codes[request.PostForm.Get("code")]
	email, subject, nonce, verified, access, refresh, scope := f.email, f.subject, f.nonceOverride, f.verified, f.access, f.refresh, f.scope
	f.mu.Unlock()
	hash := sha256.Sum256([]byte(request.PostForm.Get("code_verifier")))
	if !ok || issued.challenge != base64.RawURLEncoding.EncodeToString(hash[:]) {
		http.Error(w, "invalid authorization code or PKCE verifier", http.StatusBadRequest)
		return
	}
	if nonce == "" {
		nonce = issued.nonce
	}
	claims := jwt.MapClaims{"email": email, "email_verified": verified, "nonce": nonce, "iss": "https://accounts.google.com", "sub": subject, "aud": "client-id", "exp": time.Now().Add(time.Hour).Unix()}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "owned-google-key"
	signed, err := token.SignedString(f.key)
	if err != nil {
		http.Error(w, "sign token", http.StatusInternalServerError)
		return
	}
	if access == "" {
		access = "initial-access"
	}
	if scope == "" {
		scope = issued.scope
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"id_token": signed, "access_token": access, "refresh_token": refresh, "scope": scope, "expires_in": 3600})
}

func (f *fakeGoogle) refreshToken(w http.ResponseWriter, token string) {
	call := f.refreshCalls.Add(1)
	f.mu.Lock()
	f.refreshInputs = append(f.refreshInputs, token)
	var next fakeRefresh
	if len(f.queue) > 0 {
		next = f.queue[0]
		f.queue = f.queue[1:]
	}
	if next.scope == "" {
		next.scope = f.scope
	}
	f.mu.Unlock()
	if next.status >= 300 {
		w.WriteHeader(next.status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": next.errorCode, "error_description": "scripted owned refresh failure"})
		return
	}
	if next.access == "" {
		next.access = fmt.Sprintf("refreshed-access-%d", call)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": next.access, "refresh_token": next.refresh, "scope": next.scope, "expires_in": 3600})
}

func (f *fakeGoogle) usedRefresh(want string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, value := range f.refreshInputs {
		if value == want {
			return true
		}
	}
	return false
}

func containsScope(scopes []string, want string) bool {
	for _, scope := range scopes {
		if strings.TrimSpace(scope) == want {
			return true
		}
	}
	return false
}
