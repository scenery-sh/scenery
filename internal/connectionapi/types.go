// Package connectionapi owns the credential-free connection values shared by
// the public SDK, canonical configuration and runtime host composition.
package connectionapi

import "time"

// ProviderConfig selects trusted endpoints and permission policy. Credentials
// are environment inputs in the existing configuration store, never declarations.
type ProviderConfig struct {
	ID            string   `json:"id"`
	Preset        string   `json:"preset,omitempty"`
	Protocol      string   `json:"protocol,omitempty"`
	Issuer        string   `json:"issuer,omitempty"`
	AuthorizeURL  string   `json:"authorize_url,omitempty"`
	TokenURL      string   `json:"token_url,omitempty"`
	RevokeURL     string   `json:"revoke_url,omitempty"`
	JWKSURL       string   `json:"jwks_url,omitempty"`
	AccountURL    string   `json:"account_url,omitempty"`
	SubjectField  string   `json:"subject_field,omitempty"`
	EmailField    string   `json:"email_field,omitempty"`
	ClientAuth    string   `json:"client_auth,omitempty"`
	Resource      string   `json:"resource,omitempty"`
	AllowedScopes []string `json:"allowed_scopes"`
}

type StartParams struct {
	Scopes       []string `json:"scopes"`
	RedirectPath string   `json:"redirect_path,omitempty"`
}
type StartResponse struct {
	AuthorizeURL string `json:"authorize_url"`
	SetCookie    string `json:"-"`
}

// Status is the sole connection metadata response. It cannot carry credentials.
type Status struct {
	Provider         string     `json:"provider"`
	Status           string     `json:"status"`
	Issuer           string     `json:"issuer,omitempty"`
	Subject          string     `json:"subject,omitempty"`
	Resource         string     `json:"resource,omitempty"`
	Email            string     `json:"email,omitempty"`
	Scopes           []string   `json:"scopes,omitempty"`
	ConnectedAt      *time.Time `json:"connected_at,omitempty"`
	LastRefreshAt    *time.Time `json:"last_refresh_at,omitempty"`
	ReauthReason     string     `json:"reauth_reason,omitempty"`
	RevocationStatus string     `json:"revocation_status,omitempty"`
}
