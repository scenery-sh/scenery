package auth

import (
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
