package auth

import (
	"context"
	"testing"

	"scenery.sh/errs"
	"scenery.sh/runtime"
)

// TestRefreshReplayRevokesSessionAcrossTransaction proves the replay defense
// persists: detecting a replayed refresh token must revoke the whole session
// family, and that revocation must survive the Refresh transaction rather than
// being rolled back with it.
func TestRefreshReplayRevokesSessionAcrossTransaction(t *testing.T) {
	ctx := context.Background()
	databaseURL, cleanup := createAuthLiveTestDatabase(t, ctx)
	t.Cleanup(cleanup)
	resetStandardAuthStateForTest(t)
	t.Setenv("DATABASE_URL", databaseURL)
	t.Setenv("JWT_SECRET", "test-jwt-secret")
	runtime.SetAppConfig(runtime.AppConfig{Name: "auth-refresh-replay-test", ListenAddr: "127.0.0.1:0"})

	cfg := normalizeStandardConfig(StandardConfig{
		Enabled:               true,
		AutoBootstrapDatabase: true,
	})
	applyStandardSecrets(cfg)
	standardAuthState.mu.Lock()
	standardAuthState.cfg = cfg
	standardAuthState.mu.Unlock()
	svc, err := standardAuthService(ctx)
	if err != nil {
		t.Fatalf("standard auth service: %v", err)
	}

	signupEmailUser(t, svc, "replay@example.test", true)
	login, err := svc.LoginEmail(ctx, &EmailLoginParams{
		Email:    "replay@example.test",
		Password: "correct horse battery staple",
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	tokenZero := parseSetCookie(t, login.SetCookie).Value

	// Two legitimate rotations advance the token chain so the very first token
	// is no longer the current token or the in-grace previous token.
	first, err := svc.Refresh(ctx, &RefreshParams{RefreshToken: tokenZero})
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	tokenOne := parseSetCookie(t, first.SetCookie).Value
	second, err := svc.Refresh(ctx, &RefreshParams{RefreshToken: tokenOne})
	if err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	tokenTwo := parseSetCookie(t, second.SetCookie).Value

	// Replaying the stale first token must be rejected as unauthenticated.
	if _, err := svc.Refresh(ctx, &RefreshParams{RefreshToken: tokenZero}); errs.Code(err) != errs.Unauthenticated {
		t.Fatalf("replayed token err = %v (code %q), want unauthenticated", err, errs.Code(err))
	}

	// The legitimate current token must now be dead too: replay detection
	// revokes the whole family. Before the fix the revocation was rolled back
	// with the transaction and the current token kept working.
	if _, err := svc.Refresh(ctx, &RefreshParams{RefreshToken: tokenTwo}); err == nil {
		t.Fatal("current refresh token still valid after replay; session family was not revoked")
	}
}
