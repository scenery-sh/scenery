package assistanttoken

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"
)

const (
	initiatorPrefix = "anon1_"
	initiatorAAD    = "scenery.assistant-initiator"
	identityBytes   = 16
	maxCookieValue  = 1024
)

// IssueInitiatorCookie is the package-level form of InitiatorSigner.Issue.
func IssueInitiatorCookie(signer InitiatorSigner) (*http.Cookie, error) {
	return signer.Issue()
}

// VerifyInitiatorCookie is the package-level form of InitiatorSigner.Verify.
func VerifyInitiatorCookie(signer InitiatorSigner, cookie *http.Cookie) (AnonymousIdentity, error) {
	return signer.Verify(cookie)
}

// RotateInitiatorCookie verifies a cookie and returns a replacement when its
// key is no longer active.
func RotateInitiatorCookie(signer InitiatorSigner, cookie *http.Cookie) (AnonymousIdentity, *http.Cookie, error) {
	return signer.VerifyOrRotate(cookie)
}

// Issue creates a random anonymous identity and its provider-neutral browser
// cookie. The cookie is HttpOnly and SameSite=Lax; Secure is configurable on
// InitiatorSigner so local HTTP development remains usable.
func (s InitiatorSigner) Issue() (*http.Cookie, error) {
	identity, cookie, err := s.IssueIdentity()
	if err != nil {
		return nil, err
	}
	return identity.Cookie(cookie, s), nil
}

// IssueIdentity creates an identity and returns both its claims and cookie.
func (s InitiatorSigner) IssueIdentity() (AnonymousIdentity, string, error) {
	now := s.now()
	identity := AnonymousIdentity{Version: TokenVersion, IssuedAt: now, ExpiresAt: now.Add(s.ttl())}
	var err error
	identity.ID, err = randomHex(identityBytes)
	if err != nil {
		return AnonymousIdentity{}, "", err
	}
	value, err := s.sign(identity)
	if err != nil {
		return AnonymousIdentity{}, "", err
	}
	return identity, value, nil
}

// Verify authenticates an initiator cookie and returns its random identity.
// Invalid, expired, or unknown-key cookies return ErrNotFound.
func (s InitiatorSigner) Verify(cookie *http.Cookie) (AnonymousIdentity, error) {
	if cookie == nil || cookie.Name != CookieName {
		return AnonymousIdentity{}, ErrNotFound
	}
	return s.VerifyValue(cookie.Value)
}

// VerifyValue verifies a raw cookie value without requiring an http.Cookie.
func (s InitiatorSigner) VerifyValue(value string) (AnonymousIdentity, error) {
	identity, _, err := s.verifyValue(value)
	if err != nil {
		return AnonymousIdentity{}, ErrNotFound
	}
	return identity, nil
}

// VerifyOrRotate verifies a cookie and returns a replacement when it was
// signed by an older key. The replacement preserves the identity and expiry.
func (s InitiatorSigner) VerifyOrRotate(cookie *http.Cookie) (AnonymousIdentity, *http.Cookie, error) {
	if cookie == nil || cookie.Name != CookieName {
		return AnonymousIdentity{}, nil, ErrNotFound
	}
	identity, keyID, err := s.verifyValue(cookie.Value)
	if err != nil {
		return AnonymousIdentity{}, nil, ErrNotFound
	}
	activeID, _, activeErr := s.activeHMACKey()
	if activeErr != nil {
		return AnonymousIdentity{}, nil, ErrKeyUnavailable
	}
	if keyID == activeID {
		return identity, nil, nil
	}
	value, err := s.sign(identity)
	if err != nil {
		return AnonymousIdentity{}, nil, err
	}
	return identity, identity.Cookie(value, s), nil
}

// VerifyCookie is an explicit alias for Verify.
func (s InitiatorSigner) VerifyCookie(cookie *http.Cookie) (AnonymousIdentity, error) {
	return s.Verify(cookie)
}

// IssueCookie is an explicit alias for Issue.
func (s InitiatorSigner) IssueCookie() (*http.Cookie, error) {
	return s.Issue()
}

func (s InitiatorSigner) activeHMACKey() (string, []byte, error) {
	if s.Keys != nil {
		id, key, err := s.Keys.ActiveKey()
		if err != nil || !validKeyID(id) || len(key) == 0 {
			return "", nil, ErrKeyUnavailable
		}
		return id, append([]byte(nil), key...), nil
	}
	if len(s.Key) == 0 {
		return "", nil, ErrKeyUnavailable
	}
	id := s.KeyID
	if id == "" {
		id = "default"
	}
	if !validKeyID(id) {
		return "", nil, ErrKeyUnavailable
	}
	return id, append([]byte(nil), s.Key...), nil
}

func (s InitiatorSigner) hmacKey(id string) ([]byte, error) {
	if s.Keys != nil {
		key, err := s.Keys.Key(id)
		if err != nil || len(key) == 0 {
			return nil, ErrNotFound
		}
		return append([]byte(nil), key...), nil
	}
	activeID, key, err := s.activeHMACKey()
	if err != nil || activeID != id {
		return nil, ErrNotFound
	}
	return key, nil
}

func (s InitiatorSigner) sign(identity AnonymousIdentity) (string, error) {
	if identity.Version == 0 {
		identity.Version = TokenVersion
	}
	if identity.Version != TokenVersion || identity.ID == "" || len(identity.ID) != identityBytes*2 || identity.ID != lowerHex(identity.ID) {
		return "", errors.New("invalid anonymous identity")
	}
	if _, err := decodeLowerHex(identity.ID); err != nil {
		return "", errors.New("invalid anonymous identity")
	}
	if identity.IssuedAt.IsZero() || identity.ExpiresAt.IsZero() || !identity.ExpiresAt.After(identity.IssuedAt) {
		return "", errors.New("invalid anonymous identity expiry")
	}
	keyID, key, err := s.activeHMACKey()
	if err != nil {
		return "", err
	}
	payload := encodeIdentityPayload(identity, keyID)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(initiatorAAD + "\x00" + payload))
	return initiatorPrefix + payload + "." + hex.EncodeToString(mac.Sum(nil)), nil
}

func (s InitiatorSigner) verifyValue(value string) (AnonymousIdentity, string, error) {
	if !strings.HasPrefix(value, initiatorPrefix) {
		return AnonymousIdentity{}, "", ErrNotFound
	}
	parts := strings.Split(strings.TrimPrefix(value, initiatorPrefix), ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.ToLower(parts[0]) != parts[0] || strings.ToLower(parts[1]) != parts[1] {
		return AnonymousIdentity{}, "", ErrNotFound
	}
	payload := parts[0]
	signature, err := hex.DecodeString(parts[1])
	if err != nil || len(signature) != sha256.Size {
		return AnonymousIdentity{}, "", ErrNotFound
	}
	raw, keyID, err := decodeIdentityPayload(payload)
	if err != nil {
		return AnonymousIdentity{}, "", ErrNotFound
	}
	key, err := s.hmacKey(keyID)
	if err != nil {
		return AnonymousIdentity{}, "", ErrNotFound
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(initiatorAAD + "\x00" + payload))
	if !hmac.Equal(mac.Sum(nil), signature) {
		return AnonymousIdentity{}, "", ErrNotFound
	}
	identity, err := decodeIdentity(raw)
	now := s.now()
	if err != nil || identity.Version != TokenVersion || expired(identity.ExpiresAt, now) || identity.IssuedAt.After(now.Add(5*time.Minute)) || identity.ExpiresAt.After(identity.IssuedAt.Add(s.ttl())) {
		return AnonymousIdentity{}, "", ErrNotFound
	}
	return identity, keyID, nil
}

func encodeIdentityPayload(identity AnonymousIdentity, keyID string) string {
	id := []byte(identity.ID)
	key := []byte(keyID)
	raw := make([]byte, 1+2+len(key)+8+8+2+len(id))
	raw[0] = byte(identity.Version)
	binary.BigEndian.PutUint16(raw[1:3], uint16(len(key)))
	position := 3
	copy(raw[position:], key)
	position += len(key)
	binary.BigEndian.PutUint64(raw[position:position+8], uint64(identity.IssuedAt.Unix()))
	position += 8
	binary.BigEndian.PutUint64(raw[position:position+8], uint64(identity.ExpiresAt.Unix()))
	position += 8
	binary.BigEndian.PutUint16(raw[position:position+2], uint16(len(id)))
	position += 2
	copy(raw[position:], id)
	return hex.EncodeToString(raw)
}

func decodeIdentityPayload(value string) ([]byte, string, error) {
	if value == "" || len(value)%2 != 0 || len(value) > maxCookieValue {
		return nil, "", ErrNotFound
	}
	raw, err := hex.DecodeString(value)
	if err != nil || len(raw) < 1+2+8+8+2 {
		return nil, "", ErrNotFound
	}
	keyLength := int(binary.BigEndian.Uint16(raw[1:3]))
	position := 3
	if keyLength == 0 || keyLength > maxKeyIDBytes || len(raw) < position+keyLength+8+8+2 {
		return nil, "", ErrNotFound
	}
	keyID := string(raw[position : position+keyLength])
	if !validKeyID(keyID) {
		return nil, "", ErrNotFound
	}
	return raw, keyID, nil
}

func decodeIdentity(raw []byte) (AnonymousIdentity, error) {
	keyLength := int(binary.BigEndian.Uint16(raw[1:3]))
	position := 3 + keyLength
	issued := int64(binary.BigEndian.Uint64(raw[position : position+8]))
	position += 8
	expires := int64(binary.BigEndian.Uint64(raw[position : position+8]))
	position += 8
	idLength := int(binary.BigEndian.Uint16(raw[position : position+2]))
	position += 2
	if idLength != identityBytes*2 || len(raw) != position+idLength {
		return AnonymousIdentity{}, ErrNotFound
	}
	id := string(raw[position:])
	if id != lowerHex(id) {
		return AnonymousIdentity{}, ErrNotFound
	}
	if _, err := decodeLowerHex(id); err != nil {
		return AnonymousIdentity{}, ErrNotFound
	}
	return AnonymousIdentity{Version: int(raw[0]), ID: id, IssuedAt: time.Unix(issued, 0).UTC(), ExpiresAt: time.Unix(expires, 0).UTC()}, nil
}
