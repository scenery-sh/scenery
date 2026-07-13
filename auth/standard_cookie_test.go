package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
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
			cookie:   "scenery_refresh=current; onlv_refresh=legacy",
			want:     "explicit",
		},
		{
			name:     "invalid explicit token still wins",
			explicit: "invalid",
			cookie:   "scenery_refresh=current; onlv_refresh=legacy",
			want:     "invalid",
		},
		{
			name:     "whitespace explicit token falls back",
			explicit: "  ",
			cookie:   "scenery_refresh=current; onlv_refresh=legacy",
			want:     "current",
		},
		{
			name:   "current only",
			cookie: "scenery_refresh=current",
			want:   "current",
		},
		{
			name:   "legacy only",
			cookie: "onlv_refresh=legacy",
			want:   "legacy",
		},
		{
			name:   "current wins when both are present",
			cookie: "scenery_refresh=current; onlv_refresh=legacy",
			want:   "current",
		},
		{
			name:   "empty current blocks legacy fallback",
			cookie: "scenery_refresh=; onlv_refresh=legacy",
			want:   "",
		},
		{
			name:   "invalid current token blocks legacy fallback",
			cookie: "scenery_refresh=not-a-refresh-token; onlv_refresh=legacy",
			want:   "not-a-refresh-token",
		},
		{
			name:   "unparseable current cookie blocks legacy fallback",
			cookie: `scenery_refresh="unterminated; onlv_refresh=legacy`,
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
	if strings.Contains(setCookie, "onlv_refresh") {
		t.Fatalf("issued cookie contains legacy name: %q", setCookie)
	}

	response, err := encodeStandardContractOutcome(nil, &AuthSessionResponse{SetCookie: setCookie})
	if err != nil {
		t.Fatalf("encode auth session outcome: %v", err)
	}
	values := response.Headers.Values("Set-Cookie")
	if !reflect.DeepEqual(values, []string{setCookie}) {
		t.Fatalf("Set-Cookie values = %#v, want current cookie only", values)
	}
}

func TestLogoutClearsCurrentAndLegacyCookies(t *testing.T) {
	oldSecrets := secrets
	secrets.AuthCookieDomain = "example.test"
	secrets.APIBaseURL = ""
	t.Cleanup(func() { secrets = oldSecrets })

	response, err := (&Service{}).Logout(context.Background(), nil)
	if err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if response.SetCookie == "" || response.legacySetCookie == "" {
		t.Fatalf("logout clears = current %q legacy %q", response.SetCookie, response.legacySetCookie)
	}

	encoded, err := encodeStandardContractOutcome(nil, response)
	if err != nil {
		t.Fatalf("encode logout outcome: %v", err)
	}
	values := encoded.Headers.Values("Set-Cookie")
	wantValues := []string{response.SetCookie, response.legacySetCookie}
	if !reflect.DeepEqual(values, wantValues) {
		t.Fatalf("Set-Cookie values = %#v, want %#v", values, wantValues)
	}
	if got := string(encoded.Body); got != `{"ok":true}` {
		t.Fatalf("logout body = %q, want %q", got, `{"ok":true}`)
	}

	current := parseSetCookie(t, values[0])
	legacy := parseSetCookie(t, values[1])
	if current.Name != "scenery_refresh" || legacy.Name != "onlv_refresh" {
		t.Fatalf("cleared cookie names = %q, %q", current.Name, legacy.Name)
	}
	if current.Value != "" || legacy.Value != "" {
		t.Fatalf("cleared cookie values = %q, %q", current.Value, legacy.Value)
	}
	if current.MaxAge >= 0 || legacy.MaxAge >= 0 {
		t.Fatalf("cleared cookie MaxAge = %d, %d, want negative", current.MaxAge, legacy.MaxAge)
	}
	assertMatchingCookieScope(t, current, legacy)
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

func assertMatchingCookieScope(t *testing.T, current, legacy *http.Cookie) {
	t.Helper()
	if current.Path != legacy.Path ||
		current.Domain != legacy.Domain ||
		current.Expires != legacy.Expires ||
		current.MaxAge != legacy.MaxAge ||
		current.HttpOnly != legacy.HttpOnly ||
		current.Secure != legacy.Secure ||
		current.SameSite != legacy.SameSite {
		t.Fatalf("cookie scopes differ: current=%+v legacy=%+v", current, legacy)
	}
}
