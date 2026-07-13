package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
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
	response, err := encodeStandardContractOutcome(nil, &AuthSessionResponse{SetCookie: setCookie})
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

	encoded, err := encodeStandardContractOutcome(nil, response)
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
