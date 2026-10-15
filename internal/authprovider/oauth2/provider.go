// Package oauth2 implements configured authorization-code providers. OAuth2
// account lookup and OIDC identity verification are separate capabilities.
package oauth2

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"scenery.sh/internal/connectionvault"
	"scenery.sh/internal/oidc"
)

type Config struct {
	Issuer, AuthorizeURL, TokenURL, RevokeURL string
	ClientID, ClientSecret, ClientAuth        string
	Resource                                  string
	// OIDC uses trusted JWKS; OAuth2-only account lookup uses configured fields.
	OIDC                                          bool
	JWKSURL, AccountURL, SubjectField, EmailField string
	IssuerAliases, IdentityScopes                 []string
	AuthorizationParams                           url.Values
	ConsentPrompt                                 bool
	// VerifiedEmailOnly omits unverified OIDC email claims from connection grants.
	VerifiedEmailOnly bool
}

type Provider struct {
	config   Config
	client   http.Client
	now      func() time.Time
	identity *oidc.Validator
}

func New(config Config, client *http.Client, now func() time.Time) *Provider {
	if now == nil {
		now = time.Now
	}
	copy := http.Client{Transport: http.DefaultTransport}
	if client != nil {
		copy = *client
	}
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if config.ClientAuth == "" {
		config.ClientAuth = "basic"
	}
	if config.SubjectField == "" {
		config.SubjectField = "sub"
	}
	if config.EmailField == "" {
		config.EmailField = "email"
	}
	params := url.Values{}
	for key, values := range config.AuthorizationParams {
		params[key] = append([]string(nil), values...)
	}
	config.AuthorizationParams = params
	config.IdentityScopes = append([]string(nil), config.IdentityScopes...)
	if config.OIDC {
		config.IdentityScopes = append(config.IdentityScopes, "openid")
	}
	config.IssuerAliases = append([]string(nil), config.IssuerAliases...)
	return &Provider{config: config, client: copy, now: now, identity: &oidc.Validator{Issuers: append([]string{config.Issuer}, config.IssuerAliases...), ClientID: config.ClientID, JWKSURL: config.JWKSURL, Client: &copy, Now: now}}
}

func trustedURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	return u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")
}
func (p *Provider) configured() bool {
	c := p.config
	return c.ClientID != "" && trustedURL(c.Issuer) && trustedURL(c.AuthorizeURL) && trustedURL(c.TokenURL) &&
		(c.ClientAuth == "none" || c.ClientSecret != "" && (c.ClientAuth == "post" || c.ClientAuth == "basic")) &&
		(c.OIDC && trustedURL(c.JWKSURL) || !c.OIDC && trustedURL(c.AccountURL))
}
func providerError(kind connectionvault.FailureKind) error {
	return connectionvault.ProviderError{Kind: kind}
}

func (p *Provider) Authorize(input connectionvault.Authorization) (string, error) {
	if !p.configured() || !trustedURL(input.CallbackURI) || input.State == "" || input.Verifier == "" || p.config.OIDC && input.Nonce == "" {
		return "", providerError(connectionvault.Misconfigured)
	}
	u, _ := url.Parse(p.config.AuthorizeURL)
	q := u.Query()
	for key, values := range p.config.AuthorizationParams {
		q[key] = append([]string(nil), values...)
	}
	scopes, err := connectionvault.NormalizeScopes(append(append([]string(nil), p.config.IdentityScopes...), input.Scopes...))
	if err != nil {
		return "", providerError(connectionvault.Misconfigured)
	}
	q.Set("client_id", p.config.ClientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", input.CallbackURI)
	q.Set("state", input.State)
	q.Set("scope", strings.Join(scopes, " "))
	challenge := sha256.Sum256([]byte(input.Verifier))
	q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
	q.Set("code_challenge_method", "S256")
	if p.config.OIDC {
		q.Set("nonce", input.Nonce)
	}
	if p.config.Resource != "" {
		q.Set("resource", p.config.Resource)
	}
	if input.Consent && p.config.ConsentPrompt {
		q.Set("prompt", "consent")
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

type tokenResponse struct {
	AccessToken  string  `json:"access_token"`
	RefreshToken string  `json:"refresh_token"`
	TokenType    string  `json:"token_type"`
	ExpiresIn    *int64  `json:"expires_in"`
	Scope        *string `json:"scope"`
	IDToken      string  `json:"id_token"`
	Resource     string  `json:"resource"`
}

func (p *Provider) Exchange(ctx context.Context, input connectionvault.CodeExchange) (connectionvault.Grant, error) {
	if input.Code == "" || input.Verifier == "" || !trustedURL(input.CallbackURI) {
		return connectionvault.Grant{}, providerError(connectionvault.Misconfigured)
	}
	response, err := p.token(ctx, url.Values{"grant_type": {"authorization_code"}, "code": {input.Code}, "code_verifier": {input.Verifier}, "redirect_uri": {input.CallbackURI}})
	if err != nil {
		return connectionvault.Grant{}, err
	}
	grant, err := p.grant(response)
	if err != nil {
		return grant, err
	}
	if !strings.EqualFold(grant.TokenType, "Bearer") {
		return connectionvault.Grant{}, providerError(connectionvault.Misconfigured)
	}
	if !grant.ScopesPresent {
		grant.Scopes, err = connectionvault.NormalizeScopes(append(append([]string(nil), p.config.IdentityScopes...), input.Scopes...))
		grant.ScopesPresent = true
		if err != nil {
			return connectionvault.Grant{}, providerError(connectionvault.UnknownOutcome)
		}
	}
	if p.config.OIDC {
		if input.NonceHash == "" {
			return connectionvault.Grant{}, providerError(connectionvault.Misconfigured)
		}
		identity, err := p.identity.Verify(ctx, response.IDToken, input.NonceHash)
		if err != nil {
			return connectionvault.Grant{}, providerError(connectionvault.UnknownOutcome)
		}
		grant.Subject, grant.Email = identity.Subject, p.identityEmail(identity)
	} else {
		grant.Subject, grant.Email, err = p.account(ctx, grant.AccessToken)
		if err != nil {
			return connectionvault.Grant{}, err
		}
	}
	grant.Issuer = p.config.Issuer
	return grant, nil
}

func (p *Provider) Refresh(ctx context.Context, refresh string) (connectionvault.Grant, error) {
	response, err := p.token(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}})
	if err != nil {
		return connectionvault.Grant{}, err
	}
	grant, err := p.grant(response)
	if err != nil {
		return grant, err
	}
	if p.config.OIDC && response.IDToken != "" {
		identity, err := p.identity.Verify(ctx, response.IDToken, "")
		if err != nil {
			return connectionvault.Grant{}, providerError(connectionvault.UnknownOutcome)
		}
		grant.Subject, grant.Email, grant.Issuer = identity.Subject, p.identityEmail(identity), p.config.Issuer
	}
	return grant, nil
}

func (p *Provider) identityEmail(identity oidc.Identity) string {
	if p.config.VerifiedEmailOnly && !identity.EmailVerified {
		return ""
	}
	return identity.Email
}

func (p *Provider) token(ctx context.Context, form url.Values) (tokenResponse, error) {
	if !p.configured() {
		return tokenResponse{}, providerError(connectionvault.Misconfigured)
	}
	if p.config.Resource != "" {
		form.Set("resource", p.config.Resource)
	}
	if p.config.ClientAuth == "post" {
		form.Set("client_secret", p.config.ClientSecret)
	}
	if p.config.ClientAuth != "basic" {
		form.Set("client_id", p.config.ClientID)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.config.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, providerError(connectionvault.Misconfigured)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if p.config.ClientAuth == "basic" {
		req.SetBasicAuth(url.QueryEscape(p.config.ClientID), url.QueryEscape(p.config.ClientSecret))
	}
	response, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return tokenResponse{}, ctx.Err()
		}
		return tokenResponse{}, providerError(connectionvault.UnknownOutcome)
	}
	defer func() { _ = response.Body.Close() }()
	// Throttling explicitly refused the request, regardless of the body format.
	if response.StatusCode == http.StatusTooManyRequests {
		return tokenResponse{}, providerError(connectionvault.Temporary)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		if ctx.Err() != nil {
			return tokenResponse{}, ctx.Err()
		}
		return tokenResponse{}, providerError(connectionvault.UnknownOutcome)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var problem struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &problem)
		switch problem.Error {
		case "invalid_grant":
			return tokenResponse{}, providerError(connectionvault.Revoked)
		case "invalid_client", "unauthorized_client", "invalid_scope", "unsupported_grant_type":
			return tokenResponse{}, providerError(connectionvault.Misconfigured)
		case "temporarily_unavailable", "server_error":
			return tokenResponse{}, providerError(connectionvault.Temporary)
		default:
			return tokenResponse{}, providerError(connectionvault.UnknownOutcome)
		}
	}
	var result tokenResponse
	if json.Unmarshal(body, &result) != nil {
		return tokenResponse{}, providerError(connectionvault.UnknownOutcome)
	}
	return result, nil
}

func (p *Provider) grant(response tokenResponse) (connectionvault.Grant, error) {
	grant := connectionvault.Grant{AccessToken: response.AccessToken, RefreshToken: response.RefreshToken, TokenType: response.TokenType, Resource: p.config.Resource}
	if response.AccessToken == "" || strings.ContainsAny(response.AccessToken, "\r\n") || response.TokenType == "" || response.Resource != "" && response.Resource != p.config.Resource {
		return grant, providerError(connectionvault.UnknownOutcome)
	}
	if response.ExpiresIn != nil {
		if *response.ExpiresIn <= 0 || *response.ExpiresIn > int64((1<<63-1)/int64(time.Second)) {
			return grant, providerError(connectionvault.UnknownOutcome)
		}
		grant.ExpiresAt = p.now().Add(time.Duration(*response.ExpiresIn) * time.Second)
	}
	if response.Scope != nil {
		var err error
		grant.Scopes, err = connectionvault.ParseScopes(*response.Scope)
		grant.ScopesPresent = true
		if err != nil {
			return grant, providerError(connectionvault.UnknownOutcome)
		}
	}
	return grant, nil
}

func (p *Provider) account(ctx context.Context, access string) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.config.AccountURL, nil)
	if err != nil {
		return "", "", providerError(connectionvault.Misconfigured)
	}
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", "", ctx.Err()
		}
		return "", "", providerError(connectionvault.UnknownOutcome)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", "", providerError(connectionvault.UnknownOutcome)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return "", "", providerError(connectionvault.UnknownOutcome)
	}
	var account map[string]json.RawMessage
	if json.Unmarshal(body, &account) != nil {
		return "", "", providerError(connectionvault.UnknownOutcome)
	}
	var subject, email string
	if json.Unmarshal(account[p.config.SubjectField], &subject) != nil || subject == "" {
		return "", "", providerError(connectionvault.UnknownOutcome)
	}
	_ = json.Unmarshal(account[p.config.EmailField], &email)
	return subject, email, nil
}

func (p *Provider) Revoke(ctx context.Context, token string) error {
	if p.config.RevokeURL == "" {
		return connectionvault.ErrRevocationUnsupported
	}
	if !trustedURL(p.config.RevokeURL) || !p.configured() {
		return providerError(connectionvault.Misconfigured)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	form := url.Values{"token": {token}, "client_id": {p.config.ClientID}}
	if p.config.ClientAuth == "post" {
		form.Set("client_secret", p.config.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.config.RevokeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return providerError(connectionvault.Misconfigured)
	}
	if p.config.ClientAuth == "basic" {
		req.SetBasicAuth(url.QueryEscape(p.config.ClientID), url.QueryEscape(p.config.ClientSecret))
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.client.Do(req)
	if err != nil {
		return providerError(connectionvault.UnknownOutcome)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return providerError(connectionvault.Temporary)
	}
	return nil
}
