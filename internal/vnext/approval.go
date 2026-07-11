package vnext

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	scenery "scenery.sh"
)

const approvalTrustAPIVersion = "scenery.approval-trust.v1"

type approvalTrustStore struct {
	APIVersion string            `json:"api_version"`
	Keys       map[string]string `json:"keys"`
}

func ValidateApprovalToken(token ApprovalToken) error {
	return scenery.ValidateApprovalToken(token)
}

// LoadApprovalVerifier loads the app-local trust roots used to authenticate
// detached approval tokens. A token signature is ed25519:<key-id>:<base64>.
func LoadApprovalVerifier(root string) (ApprovalVerifier, error) {
	path := filepath.Join(root, ".scenery", "approval-trust.json")
	if err := rejectPathSymlinks(root, path); err != nil {
		return nil, fmt.Errorf("approval trust store: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("approval trust store: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("approval trust store must be a regular non-symlink file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var store approvalTrustStore
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&store); err != nil {
		return nil, fmt.Errorf("decode approval trust store: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("multiple JSON values")
		}
		return nil, fmt.Errorf("decode approval trust store: %w", err)
	}
	if store.APIVersion != approvalTrustAPIVersion || len(store.Keys) == 0 {
		return nil, fmt.Errorf("approval trust store has unsupported version or no keys")
	}
	keys := map[string]ed25519.PublicKey{}
	for id, encoded := range store.Keys {
		if strings.TrimSpace(id) == "" || strings.Contains(id, ":") || strings.IndexFunc(id, unicode.IsSpace) >= 0 {
			return nil, fmt.Errorf("approval trust store has invalid key id %q", id)
		}
		key, err := decodeApprovalBase64(encoded)
		if err != nil || len(key) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("approval trust store has invalid key %q", id)
		}
		keys[id] = ed25519.PublicKey(append([]byte(nil), key...))
	}
	return func(token ApprovalToken, canonicalPayload []byte) error {
		parts := strings.Split(token.Signature, ":")
		if len(parts) != 3 || parts[0] != "ed25519" {
			return fmt.Errorf("approval signature must use ed25519:<key-id>:<base64>")
		}
		key := keys[parts[1]]
		if key == nil {
			return fmt.Errorf("approval signing key %q is not trusted", parts[1])
		}
		signature, err := decodeApprovalBase64(parts[2])
		if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(key, canonicalPayload, signature) {
			return fmt.Errorf("approval signature verification failed")
		}
		return nil
	}, nil
}

func decodeApprovalBase64(value string) ([]byte, error) {
	if decoded, err := base64.RawStdEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	return base64.StdEncoding.DecodeString(value)
}
