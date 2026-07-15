package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	authdb "scenery.sh/auth/db/gen"
	"scenery.sh/errs"
)

const googleAccessTokenRefreshSkew = time.Minute

var googleRefreshRetryBackoff = 20 * time.Millisecond

func GoogleAccessToken(ctx context.Context, scopes ...string) (string, error) {
	data, err := currentAuthData()
	if err != nil {
		return "", unauthenticated("endpoint requires auth")
	}
	return GoogleAccessTokenForUser(ctx, string(data.UserID), scopes...)
}

func GoogleAccessTokenForUser(ctx context.Context, userID string, scopes ...string) (string, error) {
	parsedUserID, err := parseUUID(userID)
	if err != nil {
		return "", unauthenticated("invalid user id")
	}
	requested, err := validateGoogleScopes(scopes)
	if err != nil {
		return "", err
	}
	if err := validateGoogleAllowedScopes(requested); err != nil {
		return "", err
	}
	svc, err := standardAuthService(ctx)
	if err != nil {
		return "", err
	}
	return svc.googleAccessTokenForUser(ctx, parsedUserID, requested)
}

func (s *Service) googleAccessTokenForUser(ctx context.Context, userID authdb.UUID, scopes []string) (string, error) {
	tx, q, err := s.beginTx(ctx)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	conn, err := q.GetGoogleConnectionByUserForUpdate(ctx, userID)
	if err != nil {
		if isNoRows(err) {
			return "", googleReauthRequired("Google connection is not active")
		}
		return "", err
	}
	if conn.Status != "active" || len(conn.RefreshTokenCiphertext) == 0 {
		return "", googleReauthRequired("Google connection requires reauthorization")
	}
	if !googleScopesContain(parseGoogleScopes(conn.Scopes), scopes) {
		return "", googleScopeMissing("Google connection is missing required scopes")
	}
	if conn.AccessTokenExpiresAt.Valid && s.clock().Add(googleAccessTokenRefreshSkew).Before(conn.AccessTokenExpiresAt.Time) && len(conn.AccessTokenCiphertext) > 0 {
		token, err := openGoogleToken(conn.AccessTokenCiphertext)
		if err == nil && strings.TrimSpace(token) != "" {
			if err := tx.Commit(); err != nil {
				return "", err
			}
			return token, nil
		}
	}

	refreshToken, err := openGoogleToken(conn.RefreshTokenCiphertext)
	if err != nil || strings.TrimSpace(refreshToken) == "" {
		_, _ = q.MarkGoogleConnectionReauthRequired(ctx, authdb.MarkGoogleConnectionReauthRequiredParams{
			UserID:           userID,
			LastRefreshError: "token_decrypt_failed",
		})
		if commitErr := tx.Commit(); commitErr != nil {
			return "", commitErr
		}
		return "", googleReauthRequired("Google connection could not be decrypted; reconnect Google")
	}
	tokenResponse, err := refreshGoogleAccessToken(ctx, refreshToken)
	if err != nil {
		if isPermanentGoogleRefreshError(err) {
			_, _ = q.MarkGoogleConnectionReauthRequired(ctx, authdb.MarkGoogleConnectionReauthRequiredParams{
				UserID:           userID,
				LastRefreshError: googleRefreshErrorReason(err),
			})
			if commitErr := tx.Commit(); commitErr != nil {
				return "", commitErr
			}
			return "", googleReauthRequired("Google connection requires reauthorization")
		}
		return "", errs.B().Code(errs.Unavailable).Msg("Google token refresh is temporarily unavailable").Cause(err).Err()
	}
	if strings.TrimSpace(tokenResponse.AccessToken) == "" {
		return "", errs.B().Code(errs.Unavailable).Msg("Google token refresh response missing access token").Err()
	}

	refreshCipher := conn.RefreshTokenCiphertext
	if strings.TrimSpace(tokenResponse.RefreshToken) != "" {
		refreshCipher, err = sealGoogleToken(tokenResponse.RefreshToken)
		if err != nil {
			return "", err
		}
	}
	accessCipher, err := sealGoogleToken(tokenResponse.AccessToken)
	if err != nil {
		return "", err
	}
	scopesValue := conn.Scopes
	if strings.TrimSpace(tokenResponse.Scope) != "" {
		scopesValue = canonicalGoogleScopes(parseGoogleScopes(tokenResponse.Scope))
	}
	if !googleScopesContain(parseGoogleScopes(scopesValue), scopes) {
		return "", googleScopeMissing("Google refresh response is missing required scopes")
	}
	if _, err := q.UpdateGoogleConnectionTokens(ctx, authdb.UpdateGoogleConnectionTokensParams{
		UserID:                 userID,
		Scopes:                 scopesValue,
		RefreshTokenCiphertext: refreshCipher,
		AccessTokenCiphertext:  accessCipher,
		AccessTokenExpiresAt:   timestamptz(googleTokenExpiry(s.clock(), tokenResponse.ExpiresIn)),
	}); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return tokenResponse.AccessToken, nil
}

func refreshGoogleAccessToken(ctx context.Context, refreshToken string) (googleTokenResponse, error) {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		out, err := refreshGoogleAccessTokenOnce(ctx, refreshToken)
		if err == nil || isPermanentGoogleRefreshError(err) {
			return out, err
		}
		last = err
		timer := time.NewTimer(time.Duration(attempt+1) * googleRefreshRetryBackoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return googleTokenResponse{}, ctx.Err()
		case <-timer.C:
		}
	}
	return googleTokenResponse{}, last
}

func refreshGoogleAccessTokenOnce(ctx context.Context, refreshToken string) (googleTokenResponse, error) {
	form := url.Values{}
	form.Set("client_id", strings.TrimSpace(secrets.GoogleOAuthClientID))
	form.Set("client_secret", strings.TrimSpace(secrets.GoogleOAuthClientSecret))
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", strings.TrimSpace(refreshToken))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, googleTokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return googleTokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := googleHTTPClient.Do(req)
	if err != nil {
		return googleTokenResponse{}, transientGoogleRefreshError{err: err}
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		var body struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		reason := strings.TrimSpace(body.Error)
		if reason == "" {
			reason = fmt.Sprintf("status_%d", resp.StatusCode)
		}
		err := googleRefreshHTTPError{status: resp.StatusCode, reason: reason, description: strings.TrimSpace(body.ErrorDescription)}
		if resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return googleTokenResponse{}, transientGoogleRefreshError{err: err}
		}
		return googleTokenResponse{}, permanentGoogleRefreshError{err: err}
	}
	var out googleTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return googleTokenResponse{}, transientGoogleRefreshError{err: err}
	}
	return out, nil
}

type googleRefreshHTTPError struct {
	status      int
	reason      string
	description string
}

func (e googleRefreshHTTPError) Error() string {
	if e.description != "" {
		return e.reason + ": " + e.description
	}
	return e.reason
}

type transientGoogleRefreshError struct {
	err error
}

func (e transientGoogleRefreshError) Error() string { return e.err.Error() }
func (e transientGoogleRefreshError) Unwrap() error { return e.err }

type permanentGoogleRefreshError struct {
	err error
}

func (e permanentGoogleRefreshError) Error() string { return e.err.Error() }
func (e permanentGoogleRefreshError) Unwrap() error { return e.err }

func isPermanentGoogleRefreshError(err error) bool {
	var permanent permanentGoogleRefreshError
	return errors.As(err, &permanent)
}

func googleRefreshErrorReason(err error) string {
	var httpErr googleRefreshHTTPError
	if errors.As(err, &httpErr) && strings.TrimSpace(httpErr.reason) != "" {
		return httpErr.reason
	}
	return strings.TrimSpace(err.Error())
}

func googleReauthRequired(message string) error {
	return errs.B().Code(errs.GoogleReauthRequired).Msg(message).Err()
}

func googleScopeMissing(message string) error {
	return errs.B().Code(errs.GoogleScopeMissing).Msg(message).Err()
}
