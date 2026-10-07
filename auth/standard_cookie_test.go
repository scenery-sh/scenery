package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"scenery.sh/internal/appsdk"
	"scenery.sh/runtime/shared"
)

func TestResolveRefreshToken(t *testing.T) {
	tests := []struct {
		name     string
		explicit string
		cookie   string
		want     string
	}{
		{
			name:     "explicit token wins",
			explicit: " explicit ",
			cookie:   "scenery_refresh=current",
			want:     "explicit",
		},
		{
			name:     "invalid explicit token still wins",
			explicit: "invalid",
			cookie:   "scenery_refresh=current",
			want:     "invalid",
		},
		{
			name:     "whitespace explicit token falls back",
			explicit: "  ",
			cookie:   "scenery_refresh=current",
			want:     "current",
		},
		{
			name:   "current only",
			cookie: "scenery_refresh=current",
			want:   "current",
		},
		{
			name:   "empty current",
			cookie: "scenery_refresh=",
			want:   "",
		},
		{
			name:   "invalid current token is returned for validation",
			cookie: "scenery_refresh=not-a-refresh-token",
			want:   "not-a-refresh-token",
		},
		{
			name:   "unparseable current cookie",
			cookie: `scenery_refresh="unterminated`,
			want:   "",
		},
		{
			name:   "missing accepted cookies",
			cookie: "unrelated=value",
			want:   "",
		},
		{
			name: "missing cookie header",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := http.Header{}
			if tt.cookie != "" {
				headers.Set("Cookie", tt.cookie)
			}
			got := resolveRefreshToken(&RefreshParams{RefreshToken: tt.explicit}, headers)
			if got != tt.want {
				t.Fatalf("resolveRefreshToken() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRefreshCookieIssuanceStaysCurrentOnly(t *testing.T) {
	oldSecrets := secrets
	secrets.AuthCookieDomain = ""
	secrets.APIBaseURL = ""
	t.Cleanup(func() { secrets = oldSecrets })

	setCookie := refreshCookie(" token ", time.Now().Add(time.Hour))
	cookie := parseSetCookie(t, setCookie)
	if cookie.Name != "scenery_refresh" {
		t.Fatalf("issued cookie name = %q, want scenery_refresh", cookie.Name)
	}
	if cookie.Value != "token" {
		t.Fatalf("issued cookie value = %q, want token", cookie.Value)
	}
	response, err := encodeStandardContractOutcome(appsdk.CurrentHost(), &AuthSessionResponse{SetCookie: setCookie})
	if err != nil {
		t.Fatalf("encode auth session outcome: %v", err)
	}
	values := response.Headers.Values("Set-Cookie")
	if !reflect.DeepEqual(values, []string{setCookie}) {
		t.Fatalf("Set-Cookie values = %#v, want current cookie only", values)
	}
}

func TestLogoutClearsRefreshCookie(t *testing.T) {
	oldSecrets := secrets
	secrets.AuthCookieDomain = "example.test"
	secrets.APIBaseURL = ""
	t.Cleanup(func() { secrets = oldSecrets })

	response, err := (&Service{}).Logout(context.Background(), nil)
	if err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if response.SetCookie == "" {
		t.Fatal("logout did not clear refresh cookie")
	}

	encoded, err := encodeStandardContractOutcome(appsdk.CurrentHost(), response)
	if err != nil {
		t.Fatalf("encode logout outcome: %v", err)
	}
	values := encoded.Headers.Values("Set-Cookie")
	wantValues := []string{response.SetCookie}
	if !reflect.DeepEqual(values, wantValues) {
		t.Fatalf("Set-Cookie values = %#v, want %#v", values, wantValues)
	}
	if got := string(encoded.Body); got != `{"ok":true}` {
		t.Fatalf("logout body = %q, want %q", got, `{"ok":true}`)
	}

	current := parseSetCookie(t, values[0])
	if current.Name != "scenery_refresh" {
		t.Fatalf("cleared cookie name = %q", current.Name)
	}
	if current.Value != "" {
		t.Fatalf("cleared cookie value = %q", current.Value)
	}
	if current.MaxAge >= 0 {
		t.Fatalf("cleared cookie MaxAge = %d, want negative", current.MaxAge)
	}
}

func parseSetCookie(t *testing.T, value string) *http.Cookie {
	t.Helper()
	recorder := httptest.NewRecorder()
	recorder.Header().Add("Set-Cookie", value)
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("parsed cookies = %#v, want one from %q", cookies, value)
	}
	return cookies[0]
}

func TestRefreshCookieNameScopesLocalRuntimes(t *testing.T) {
	oldMeta := appsdk.Metadata()
	t.Cleanup(func() { appsdk.SetMetadata(*oldMeta) })

	local := func(baseURL string) {
		appsdk.SetMetadata(shared.AppMetadata{APIBaseURL: baseURL, Environment: shared.Environment{Name: "local", Type: shared.EnvDevelopment, Cloud: shared.CloudLocal}})
	}
	local("http://localhost:4976")
	if got := refreshCookieName(); got != "scenery_refresh_localhost_4976" {
		t.Fatalf("local refreshCookieName() = %q", got)
	}
	local("http://localhost:4920")
	if got := refreshCookieName(); got != "scenery_refresh_localhost_4920" {
		t.Fatalf("second local refreshCookieName() = %q", got)
	}
	local("http://Clean-Tech.local.dev:4920/api")
	if got := refreshCookieName(); got != "scenery_refresh_clean_tech_local_dev_4920" {
		t.Fatalf("named host refreshCookieName() = %q", got)
	}
	local("")
	if got := refreshCookieName(); got != "scenery_refresh" {
		t.Fatalf("local refreshCookieName() without base URL = %q", got)
	}
	appsdk.SetMetadata(shared.AppMetadata{APIBaseURL: "https://api.example.test", Environment: shared.Environment{Name: "production", Type: shared.EnvProduction, Cloud: shared.CloudGCP}})
	if got := refreshCookieName(); got != "scenery_refresh" {
		t.Fatalf("deployed refreshCookieName() = %q", got)
	}
}

func TestScopedRefreshCookieIsolatesLocalRuntimes(t *testing.T) {
	oldMeta := appsdk.Metadata()
	oldSecrets := secrets
	secrets.AuthCookieDomain = ""
	secrets.APIBaseURL = ""
	t.Cleanup(func() {
		appsdk.SetMetadata(*oldMeta)
		secrets = oldSecrets
	})
	appsdk.SetMetadata(shared.AppMetadata{APIBaseURL: "http://localhost:4976", Environment: shared.Environment{Name: "local", Type: shared.EnvDevelopment, Cloud: shared.CloudLocal}})

	issued := parseSetCookie(t, refreshCookie("token", time.Now().Add(time.Hour)))
	if issued.Name != "scenery_refresh_localhost_4976" {
		t.Fatalf("issued cookie name = %q", issued.Name)
	}
	cleared := parseSetCookie(t, clearRefreshCookie())
	if cleared.Name != issued.Name || cleared.MaxAge >= 0 {
		t.Fatalf("cleared cookie = %q MaxAge=%d, want %q cleared", cleared.Name, cleared.MaxAge, issued.Name)
	}

	headers := http.Header{}
	headers.Set("Cookie", "scenery_refresh=other-runtime; scenery_refresh_localhost_4920=sibling; scenery_refresh_localhost_4976=mine")
	if got := resolveRefreshToken(nil, headers); got != "mine" {
		t.Fatalf("resolveRefreshToken() = %q, want the runtime's own cookie", got)
	}
	headers.Set("Cookie", "scenery_refresh=other-runtime; scenery_refresh_localhost_4920=sibling")
	if got := resolveRefreshToken(nil, headers); got != "" {
		t.Fatalf("resolveRefreshToken() = %q, want no token from other runtimes' cookies", got)
	}
}
