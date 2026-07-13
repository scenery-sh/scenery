package auth

import "time"

const (
	defaultAccessTokenTTL       = 15 * time.Minute
	defaultRefreshSessionTTL    = 30 * 24 * time.Hour
	defaultImpersonationTTL     = 30 * time.Minute
	defaultPasswordResetTTL     = 30 * time.Minute
	defaultEmailVerificationTTL = 24 * time.Hour
	defaultInviteTTL            = 7 * 24 * time.Hour
	defaultOAuthStateTTL        = 10 * time.Minute
	googleJWKSCacheTTL          = time.Hour
	refreshTokenReplayGrace     = 30 * time.Second
)

const (
	refreshCookieName       = "scenery_refresh"
	legacyRefreshCookieName = "onlv_refresh"
)

// refreshCookieReadOrder is temporary compatibility tracked by plan 0110. Remove
// the legacy entry no earlier than 30 days after the corrective release ships.
var refreshCookieReadOrder = [...]string{refreshCookieName, legacyRefreshCookieName}

const (
	identityProviderEmail  = "email"
	identityProviderGoogle = "google"
)

const (
	tokenPurposeEmailVerification = "email_verification"
	tokenPurposePasswordReset     = "password_reset"
	tokenPurposeInviteAcceptance  = "invite_acceptance"
)

const (
	roleOwner  = "owner"
	roleMember = "member"
)
