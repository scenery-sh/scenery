package auth

import (
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"scenery.sh/errs"
)

func TestInvalidAccessTokensAreUnauthenticated(t *testing.T) {
	previous := secrets.JWTSecret
	secrets.JWTSecret = "token-classification-test"
	t.Cleanup(func() { secrets.JWTSecret = previous })
	sign := func(claims jwt.MapClaims, key string) string {
		t.Helper()
		token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(key))
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	validClaims := jwt.MapClaims{"sub": "user", "tenant_id": "tenant", "exp": time.Now().Add(time.Hour).Unix()}
	for name, token := range map[string]string{
		"malformed": "invalid",
		"signature": sign(validClaims, "wrong-key"),
		"expired":   sign(jwt.MapClaims{"sub": "user", "tenant_id": "tenant", "exp": 1}, secrets.JWTSecret),
		"subject":   sign(jwt.MapClaims{"tenant_id": "tenant", "exp": validClaims["exp"]}, secrets.JWTSecret),
		"tenant":    sign(jwt.MapClaims{"sub": "user", "exp": validClaims["exp"]}, secrets.JWTSecret),
	} {
		t.Run(name, func(t *testing.T) {
			uid, data, err := AuthHandler(t.Context(), token)
			if uid != "" || data != nil || errs.Code(err) != errs.Unauthenticated {
				t.Fatalf("invalid credential = %q, %#v, %v", uid, data, err)
			}
		})
	}
	secrets.JWTSecret = ""
	if _, err := ValidateToken("invalid"); errs.Code(err) == errs.Unauthenticated || err == nil {
		t.Fatalf("missing server configuration must remain a server error: %v", err)
	}
}

func TestReloadStandardSecretsRotatesJWTSigningKeyConcurrently(t *testing.T) {
	secretsMu.Lock()
	prevSecrets := secrets
	secretsMu.Unlock()
	prevSecretReader := configuredAuthSecret
	prevStringReader := configuredAuthString
	standardAuthState.mu.Lock()
	prevCfg := standardAuthState.cfg
	standardAuthState.cfg = StandardConfig{Enabled: true}
	standardAuthState.mu.Unlock()
	t.Cleanup(func() {
		configuredAuthSecret = prevSecretReader
		configuredAuthString = prevStringReader
		standardAuthState.mu.Lock()
		standardAuthState.cfg = prevCfg
		standardAuthState.mu.Unlock()
		secretsMu.Lock()
		secrets = prevSecrets
		secretsMu.Unlock()
	})

	var cfgMu sync.RWMutex
	jwtValue := "secret-one"
	clientIDValue := "client-one"
	configuredAuthSecret = func(key string) ([]byte, bool, error) {
		cfgMu.RLock()
		defer cfgMu.RUnlock()
		if key == "auth.jwt_secret" {
			return []byte(jwtValue), true, nil
		}
		return nil, false, nil
	}
	configuredAuthString = func(key string) (string, bool, error) {
		cfgMu.RLock()
		defer cfgMu.RUnlock()
		if key == "auth.google_client_id" {
			return clientIDValue, true, nil
		}
		return "", false, nil
	}
	if err := ReloadStandardSecrets(); err != nil {
		t.Fatal(err)
	}
	tokenOne, err := GenerateAccessToken(AccessTokenOptions{UserID: "u1", TenantID: "t1", ExpiresIn: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateToken(tokenOne); err != nil {
		t.Fatalf("ValidateToken(tokenOne) before rotation = %v", err)
	}

	cfgMu.Lock()
	jwtValue = "secret-two"
	clientIDValue = "client-two"
	cfgMu.Unlock()
	if err := ReloadStandardSecrets(); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateToken(tokenOne); err == nil {
		t.Fatal("ValidateToken(tokenOne) unexpectedly accepted rotated-out key")
	}
	if got := currentStandardSecrets().GoogleOAuthClientID; got != "client-two" {
		t.Fatalf("GoogleOAuthClientID after reload = %q, want client-two", got)
	}
	tokenTwo, err := GenerateAccessToken(AccessTokenOptions{UserID: "u1", TenantID: "t1", ExpiresIn: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateToken(tokenTwo); err != nil {
		t.Fatalf("ValidateToken(tokenTwo) = %v", err)
	}

	var wg sync.WaitGroup
	wg.Go(func() {
		for range 20 {
			_ = ReloadStandardSecrets()
		}
	})
	wg.Go(func() {
		for range 20 {
			_, _ = ValidateToken(tokenTwo)
		}
	})
	wg.Wait()
}
