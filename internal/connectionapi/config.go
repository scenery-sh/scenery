package connectionapi

import (
	"fmt"
	"net/url"
	"regexp"
)

var providerIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

func trustedEndpoint(value string) bool {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return false
	}
	return u.Scheme == "https" || u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")
}

// ValidateProviders checks declarations without credentials, discovery or IO.
func ValidateProviders(providers []ProviderConfig) error {
	seen := map[string]bool{}
	for _, p := range providers {
		if !providerIDPattern.MatchString(p.ID) || seen[p.ID] {
			return fmt.Errorf("auth.connections provider IDs must be unique lowercase configuration identifiers")
		}
		seen[p.ID] = true
		if _, err := NormalizeScopes(p.AllowedScopes); err != nil {
			return fmt.Errorf("auth.connections.%s has invalid allowed scopes", p.ID)
		}
		if p.Preset != "" && p.Preset != "google" {
			return fmt.Errorf("auth.connections.%s preset is unsupported", p.ID)
		}
		if p.Preset == "google" {
			if p.Protocol != "" || p.Issuer != "" || p.AccountURL != "" || p.ClientAuth != "" || p.Resource != "" || p.SubjectField != "" || p.EmailField != "" {
				return fmt.Errorf("auth.connections.%s Google preset owns its protocol, issuer and client authentication and does not support resource selection", p.ID)
			}
		} else {
			if p.Protocol != "oauth2" && p.Protocol != "oidc" {
				return fmt.Errorf("auth.connections.%s protocol must be oauth2 or oidc", p.ID)
			}
			if !trustedEndpoint(p.Issuer) || !trustedEndpoint(p.AuthorizeURL) || !trustedEndpoint(p.TokenURL) {
				return fmt.Errorf("auth.connections.%s requires trusted issuer, authorize and token URLs", p.ID)
			}
			if p.Protocol == "oidc" && !trustedEndpoint(p.JWKSURL) || p.Protocol == "oauth2" && !trustedEndpoint(p.AccountURL) {
				return fmt.Errorf("auth.connections.%s requires its configured identity endpoint", p.ID)
			}
			if p.ClientAuth != "" && p.ClientAuth != "basic" && p.ClientAuth != "post" && p.ClientAuth != "none" {
				return fmt.Errorf("auth.connections.%s client_auth must be basic, post or none", p.ID)
			}
		}
		for _, value := range []string{p.AuthorizeURL, p.TokenURL, p.RevokeURL, p.JWKSURL, p.AccountURL} {
			if value != "" && !trustedEndpoint(value) {
				return fmt.Errorf("auth.connections.%s endpoint must use HTTPS or loopback HTTP without URL credentials or fragments", p.ID)
			}
		}
	}
	return nil
}
