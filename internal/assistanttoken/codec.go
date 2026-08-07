package assistanttoken

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	conversationAAD = "scenery.assistant-token/conversation/v1"
	approvalAAD     = "scenery.assistant-token/approval/v1"
	maxKeyIDBytes   = 128
	// Public assistant IDs are carried in path segments and assistantapi caps
	// their lowercase-hex suffixes at 8 KiB. Keep the encrypted envelope
	// bounded in bytes so the hex representation cannot exceed that contract.
	maxTokenHexBytes = 8 << 10
	maxEnvelopeSize  = maxTokenHexBytes / 2
	maxClaimBytes    = 4 << 10
	maxStringBytes   = 1 << 10
	nonceBytes       = 16
)

func (m Manager) now() time.Time {
	if m.Now != nil {
		return m.Now().UTC()
	}
	return time.Now().UTC()
}

func (m Manager) ttl() time.Duration {
	if m.TTL > 0 {
		return m.TTL
	}
	return DefaultTokenTTL
}

func (s InitiatorSigner) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s InitiatorSigner) ttl() time.Duration {
	if s.TTL > 0 {
		if s.TTL > MaxCookieTTL {
			return MaxCookieTTL
		}
		return s.TTL
	}
	return DefaultTokenTTL
}

func activeKey(keys Keyring) (string, []byte, error) {
	if keys == nil {
		return "", nil, ErrKeyUnavailable
	}
	id, key, err := keys.ActiveKey()
	if err != nil || !validKeyID(id) || !validAESKey(key) {
		return "", nil, ErrKeyUnavailable
	}
	return id, append([]byte(nil), key...), nil
}

func keyFor(keys Keyring, id string) ([]byte, error) {
	if keys == nil || !validKeyID(id) {
		return nil, ErrNotFound
	}
	key, err := keys.Key(id)
	if err != nil || !validAESKey(key) {
		return nil, ErrNotFound
	}
	return append([]byte(nil), key...), nil
}

func validAESKey(key []byte) bool {
	switch len(key) {
	case 16, 24, 32:
		return true
	default:
		return false
	}
}

func validKeyID(id string) bool {
	if id == "" || len(id) > maxKeyIDBytes || !utf8.ValidString(id) {
		return false
	}
	for _, char := range id {
		if char <= ' ' || char == ':' || char == '/' || char == '\\' {
			return false
		}
	}
	return true
}

func randomHex(size int) (string, error) {
	bytesValue := make([]byte, size)
	if _, err := rand.Read(bytesValue); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytesValue), nil
}

func normalizeClaimsTimes(version *int, issuedAt, expiresAt *time.Time, now time.Time, ttl time.Duration) error {
	if *version == 0 {
		*version = TokenVersion
	}
	if *version != TokenVersion {
		return errors.New("unsupported assistant token version")
	}
	if issuedAt.IsZero() {
		*issuedAt = now
	} else {
		*issuedAt = issuedAt.UTC()
	}
	if expiresAt.IsZero() {
		*expiresAt = issuedAt.Add(ttl)
	} else {
		*expiresAt = expiresAt.UTC()
	}
	if expiresAt.IsZero() || !expiresAt.After(*issuedAt) || !expiresAt.After(now) {
		return errors.New("assistant token expiry must be after issue time and now")
	}
	if expiresAt.After(issuedAt.Add(ttl)) {
		return errors.New("assistant token expiry exceeds manager TTL")
	}
	if issuedAt.After(now.Add(5 * time.Minute)) {
		return errors.New("assistant token issue time is too far in the future")
	}
	return nil
}

func validateTokenString(name, value string, required bool) error {
	if value == "" {
		if required {
			return fmt.Errorf("%s is required", name)
		}
		return nil
	}
	if len(value) > maxStringBytes || !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
		return fmt.Errorf("%s is invalid", name)
	}
	return nil
}

func validateConversationClaims(claims *ConversationClaims, now time.Time, ttl time.Duration) error {
	if err := normalizeClaimsTimes(&claims.Version, &claims.IssuedAt, &claims.ExpiresAt, now, ttl); err != nil {
		return err
	}
	for _, field := range []struct {
		name, value string
	}{
		{"assistant_address", claims.AssistantAddress},
		{"owner_digest", claims.OwnerDigest},
		{"private_session_id", claims.PrivateSessionID},
		{"continuation_token", claims.ContinuationToken},
	} {
		if err := validateTokenString(field.name, field.value, true); err != nil {
			return err
		}
	}
	if claims.Nonce == "" {
		var err error
		claims.Nonce, err = randomHex(nonceBytes)
		if err != nil {
			return fmt.Errorf("generate conversation nonce: %w", err)
		}
	}
	if len(claims.Nonce) != nonceBytes*2 || strings.ToLower(claims.Nonce) != claims.Nonce {
		return errors.New("conversation nonce must be lowercase hex")
	}
	if _, err := hex.DecodeString(claims.Nonce); err != nil {
		return errors.New("conversation nonce must be lowercase hex")
	}
	return nil
}

func validateApprovalClaims(claims *ApprovalClaims, now time.Time, ttl time.Duration) error {
	if err := normalizeClaimsTimes(&claims.Version, &claims.IssuedAt, &claims.ExpiresAt, now, ttl); err != nil {
		return err
	}
	for _, field := range []struct {
		name, value string
	}{
		{"assistant_address", claims.AssistantAddress},
		{"conversation_digest", claims.ConversationDigest},
		{"run_id", claims.RunID},
		{"approval_id", claims.ApprovalID},
		{"owner_digest", claims.OwnerDigest},
		{"decision_context", claims.DecisionContext},
	} {
		if err := validateTokenString(field.name, field.value, true); err != nil {
			return err
		}
	}
	if claims.Nonce == "" {
		var err error
		claims.Nonce, err = randomHex(nonceBytes)
		if err != nil {
			return fmt.Errorf("generate approval nonce: %w", err)
		}
	}
	if len(claims.Nonce) != nonceBytes*2 || strings.ToLower(claims.Nonce) != claims.Nonce {
		return errors.New("approval nonce must be lowercase hex")
	}
	if _, err := hex.DecodeString(claims.Nonce); err != nil {
		return errors.New("approval nonce must be lowercase hex")
	}
	return nil
}

func marshalClaims(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(encoded) > maxClaimBytes {
		return nil, errors.New("assistant token claims exceed size limit")
	}
	return encoded, nil
}

func unmarshalClaims(data []byte, value any) error {
	if len(data) == 0 || len(data) > maxClaimBytes {
		return ErrNotFound
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return ErrNotFound
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return ErrNotFound
	}
	return nil
}

func makeAAD(base, keyID string) []byte {
	return []byte(base + "\x00" + keyID)
}

func sealEnvelope(prefix, aadBase string, keys Keyring, plaintext []byte) (string, error) {
	keyID, key, err := activeKey(keys)
	if err != nil {
		return "", err
	}
	cipherBlock, err := aes.NewCipher(key)
	if err != nil {
		return "", ErrKeyUnavailable
	}
	gcm, err := cipher.NewGCM(cipherBlock)
	if err != nil {
		return "", ErrKeyUnavailable
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate token nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, makeAAD(aadBase, keyID))
	if len(keyID) > 0xffff {
		return "", ErrKeyUnavailable
	}
	envelope := make([]byte, 2+len(keyID)+len(nonce)+len(ciphertext))
	if len(envelope)*2 > maxTokenHexBytes {
		return "", errors.New("assistant token envelope exceeds size limit")
	}
	binary.BigEndian.PutUint16(envelope[:2], uint16(len(keyID)))
	position := 2
	copy(envelope[position:], keyID)
	position += len(keyID)
	copy(envelope[position:], nonce)
	position += len(nonce)
	copy(envelope[position:], ciphertext)
	return prefix + hex.EncodeToString(envelope), nil
}

func openEnvelope(prefix, aadBase, token string, keys Keyring) ([]byte, string, error) {
	if !strings.HasPrefix(token, prefix) {
		return nil, "", ErrNotFound
	}
	encoded := strings.TrimPrefix(token, prefix)
	if encoded == "" || len(encoded)%2 != 0 || len(encoded) > maxEnvelopeSize*2 || strings.ToLower(encoded) != encoded {
		return nil, "", ErrNotFound
	}
	envelope, err := hex.DecodeString(encoded)
	if err != nil || len(envelope) < 2 {
		return nil, "", ErrNotFound
	}
	idLength := int(binary.BigEndian.Uint16(envelope[:2]))
	if idLength == 0 || idLength > maxKeyIDBytes || len(envelope) <= 2+idLength {
		return nil, "", ErrNotFound
	}
	keyID := string(envelope[2 : 2+idLength])
	if !validKeyID(keyID) {
		return nil, "", ErrNotFound
	}
	key, err := keyFor(keys, keyID)
	if err != nil {
		return nil, "", ErrNotFound
	}
	cipherBlock, err := aes.NewCipher(key)
	if err != nil {
		return nil, "", ErrNotFound
	}
	gcm, err := cipher.NewGCM(cipherBlock)
	if err != nil {
		return nil, "", ErrNotFound
	}
	position := 2 + idLength
	if len(envelope) < position+gcm.NonceSize()+gcm.Overhead() {
		return nil, "", ErrNotFound
	}
	nonce := envelope[position : position+gcm.NonceSize()]
	ciphertext := envelope[position+gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, makeAAD(aadBase, keyID))
	if err != nil {
		return nil, "", ErrNotFound
	}
	return plaintext, keyID, nil
}

func expired(expiresAt time.Time, now time.Time) bool {
	return expiresAt.IsZero() || !expiresAt.After(now)
}

func equalString(left, right string) bool {
	if left == "" || right == "" {
		return left == right
	}
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

// OwnerDigest returns a stable, provider-neutral digest suitable for binding
// a public token to an authenticated or anonymous initiator identity.
func OwnerDigest(owner string) string {
	hash := sha256.Sum256(append([]byte("scenery.assistant-owner\x00"), []byte(owner)...))
	return "sha256:" + hex.EncodeToString(hash[:])
}

// ConversationDigest returns a stable, provider-neutral binding for one
// public conversation. It is deliberately domain-separated from owner
// identity so two conversations created by one principal cannot resume the
// same private helper session.
func ConversationDigest(identity string) string {
	hash := sha256.Sum256(append([]byte("scenery.assistant-conversation\x00"), []byte(identity)...))
	return "sha256:" + hex.EncodeToString(hash[:])
}
