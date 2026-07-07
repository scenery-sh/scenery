package auth

import (
	"net/http"
	"testing"
)

func TestRefreshCookieSecureUsesSecureCookieForLocalHTTPS(t *testing.T) {
	t.Parallel()

	headers := http.Header{"X-Forwarded-Proto": []string{"https"}}
	secure := refreshCookieSecure(true, headers, "")

	if !secure {
		t.Fatalf("secure = false, want true")
	}
}

func TestRefreshCookieSecureKeepsLocalHTTPDevelopmentCookieInsecure(t *testing.T) {
	t.Parallel()

	headers := http.Header{"X-Forwarded-Proto": []string{"http"}}
	secure := refreshCookieSecure(true, headers, "")

	if secure {
		t.Fatalf("secure = true, want false")
	}
}

func TestRefreshCookieSecureKeepsNonLocalCookieSecure(t *testing.T) {
	t.Parallel()

	headers := http.Header{"X-Forwarded-Proto": []string{"https"}}
	secure := refreshCookieSecure(false, headers, "")

	if !secure {
		t.Fatalf("secure = false, want true")
	}
}

func TestRefreshCookieSecureUsesSecureCookieForLocalHTTPSAPIBaseURL(t *testing.T) {
	t.Parallel()

	secure := refreshCookieSecure(true, nil, "https://api.main-dbe32e.onlv.localhost/")

	if !secure {
		t.Fatalf("secure = false, want true")
	}
}

func TestRefreshCookieSecureUsesSecureCookieForLocalHTTPSOrigin(t *testing.T) {
	t.Parallel()

	headers := http.Header{"Origin": []string{"https://pulse.main-dbe32e.onlv.localhost"}}
	secure := refreshCookieSecure(true, headers, "")

	if !secure {
		t.Fatalf("secure = false, want true")
	}
}

func TestRefreshCookiePathIncludesForwardedAPIPrefix(t *testing.T) {
	if got, want := refreshCookiePath(http.Header{"X-Forwarded-Prefix": []string{"/api"}}), "/api/auth"; got != want {
		t.Fatalf("refresh cookie path = %q, want %q", got, want)
	}
}
