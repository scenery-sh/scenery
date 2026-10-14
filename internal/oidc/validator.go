// Package oidc verifies account identity independently of application sign-in.
package oidc

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

var ErrInvalidIdentity = errors.New("provider identity could not be verified")

type Identity struct {
	Issuer          string          `json:"iss"`
	Subject         string          `json:"sub"`
	Audience        json.RawMessage `json:"aud"`
	AuthorizedParty string          `json:"azp"`
	ExpiresAt       int64           `json:"exp"`
	IssuedAt        int64           `json:"iat"`
	Nonce           string          `json:"nonce"`
	Email           string          `json:"email"`
	EmailVerified   bool            `json:"email_verified"`
}

// Validator is instance-owned. Construction does no discovery or network IO;
// runtime supplies trusted issuer/JWKS configuration and a bounded HTTP client.
type Validator struct {
	Issuers           []string
	ClientID, JWKSURL string
	Client            *http.Client
	Now               func() time.Time
	mu                sync.Mutex
	keys              map[string]*rsa.PublicKey
	keysUntil         time.Time
}

func (v *Validator) Verify(ctx context.Context, token, nonceHash string) (Identity, error) {
	invalid := func() (Identity, error) { return Identity{}, ErrInvalidIdentity }
	parts := strings.Split(token, ".")
	if len(parts) != 3 || len(token) > 1<<20 {
		return invalid()
	}
	decode := func(s string, out any) error {
		raw, err := base64.RawURLEncoding.DecodeString(s)
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, out)
	}
	var header struct {
		Algorithm string `json:"alg"`
		KeyID     string `json:"kid"`
	}
	var identity Identity
	if decode(parts[0], &header) != nil || decode(parts[1], &identity) != nil || header.Algorithm != "RS256" || header.KeyID == "" {
		return invalid()
	}
	now := time.Now()
	if v.Now != nil {
		now = v.Now()
	}
	if !slices.Contains(v.Issuers, identity.Issuer) || identity.Subject == "" || identity.ExpiresAt <= now.Unix() || identity.IssuedAt > now.Add(30*time.Second).Unix() {
		return invalid()
	}
	var audiences []string
	var one string
	if json.Unmarshal(identity.Audience, &one) == nil {
		audiences = []string{one}
	} else if json.Unmarshal(identity.Audience, &audiences) != nil {
		return invalid()
	}
	if v.ClientID == "" || !slices.Contains(audiences, v.ClientID) || len(audiences) > 1 && identity.AuthorizedParty != v.ClientID || identity.AuthorizedParty != "" && identity.AuthorizedParty != v.ClientID {
		return invalid()
	}
	if nonceHash != "" {
		digest := sha256.Sum256([]byte(identity.Nonce))
		if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(digest[:])), []byte(nonceHash)) != 1 {
			return invalid()
		}
	}
	key, err := v.key(ctx, header.KeyID, now)
	if err != nil {
		return invalid()
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return invalid()
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature) != nil {
		return invalid()
	}
	return identity, nil
}

func (v *Validator) key(ctx context.Context, kid string, now time.Time) (*rsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if key := v.keys[kid]; key != nil && now.Before(v.keysUntil) {
		return key, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.JWKSURL, nil)
	if err != nil {
		return nil, ErrInvalidIdentity
	}
	client := http.Client{Transport: http.DefaultTransport}
	if v.Client != nil {
		client = *v.Client
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return nil, ErrInvalidIdentity
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, ErrInvalidIdentity
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return nil, ErrInvalidIdentity
	}
	var set struct {
		Keys []struct {
			KeyID     string `json:"kid"`
			Type      string `json:"kty"`
			Algorithm string `json:"alg"`
			Use       string `json:"use"`
			N         string `json:"n"`
			E         string `json:"e"`
		} `json:"keys"`
	}
	if json.Unmarshal(body, &set) != nil || len(set.Keys) > 100 {
		return nil, ErrInvalidIdentity
	}
	keys := map[string]*rsa.PublicKey{}
	for _, entry := range set.Keys {
		if entry.Type != "RSA" || entry.Algorithm != "" && entry.Algorithm != "RS256" || entry.Use != "" && entry.Use != "sig" || entry.KeyID == "" {
			continue
		}
		n, err := base64.RawURLEncoding.DecodeString(entry.N)
		if err != nil || len(n) < 256 || len(n) > 1024 {
			continue
		}
		e, err := base64.RawURLEncoding.DecodeString(entry.E)
		if err != nil || len(e) == 0 || len(e) > 4 {
			continue
		}
		exponent := new(big.Int).SetBytes(e).Int64()
		if exponent < 3 || exponent > 1<<31-1 || exponent%2 == 0 {
			continue
		}
		if keys[entry.KeyID] != nil {
			return nil, ErrInvalidIdentity
		}
		keys[entry.KeyID] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(exponent)}
	}
	v.keys, v.keysUntil = keys, now.Add(time.Hour)
	if key := keys[kid]; key != nil {
		return key, nil
	}
	return nil, ErrInvalidIdentity
}
