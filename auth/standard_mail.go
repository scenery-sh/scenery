package auth

import (
	"context"
	"net/url"
	"strings"
)

func sendVerificationEmail(ctx context.Context, email string, token string, redirectPath string) error {
	query := url.Values{}
	query.Set("token", token)
	query.Set("redirect_path", safeRedirectPath(redirectPath))
	return sendAuthEmail(ctx, email, "Verify your email", "verify your email", appRedirectURL("/verify-email?"+query.Encode()))
}

func sendPasswordResetEmail(ctx context.Context, email string, token string) error {
	query := url.Values{}
	query.Set("token", token)
	return sendAuthEmail(ctx, email, "Reset your password", "reset your password", appRedirectURL("/reset-password?"+query.Encode()))
}

func sendInviteEmail(ctx context.Context, email string, token string) error {
	query := url.Values{}
	query.Set("token", token)
	return sendAuthEmail(ctx, email, "You have been invited", "accept your invite", appRedirectURL("/accept-invite?"+query.Encode()))
}

func sendAuthEmail(ctx context.Context, to string, subject string, action string, link string) error {
	from := strings.TrimSpace(secrets.AuthEmailFrom)
	if from == "" {
		return nil
	}
	return defaultEmailSender(ctx, EmailMessage{
		From:    from,
		To:      []string{strings.TrimSpace(to)},
		Subject: subject,
		Text:    "Open this link to " + action + ": " + link,
	})
}
