// Package google configures the OAuth2/OIDC adapter for Google's behavior.
// The vault does not depend on this preset or on Google scope/endpoints.
package google

import (
	"net/url"

	"scenery.sh/internal/authprovider/oauth2"
)

func Config(clientID, clientSecret, resource string) oauth2.Config {
	return oauth2.Config{
		Issuer: "https://accounts.google.com", IssuerAliases: []string{"accounts.google.com"},
		AuthorizeURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token",
		RevokeURL: "https://oauth2.googleapis.com/revoke", JWKSURL: "https://www.googleapis.com/oauth2/v3/certs",
		ClientID: clientID, ClientSecret: clientSecret, ClientAuth: "post", Resource: resource, OIDC: true,
		IdentityScopes:      []string{"openid", "email", "profile"},
		ConsentPrompt:       true,
		AuthorizationParams: url.Values{"access_type": {"offline"}, "include_granted_scopes": {"true"}},
	}
}
