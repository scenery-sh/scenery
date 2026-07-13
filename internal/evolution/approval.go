package evolution

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	scenery "scenery.sh"
	"scenery.sh/internal/machine"
	"scenery.sh/internal/scn"
)

const (
	legacyApprovalTrustAPIVersion = "scenery.approval-trust.v1"
	approvalTrustBackupSuffix     = ".legacy-v1.bak"
	approvalTrustMarkerSuffix     = ".legacy-v1.migrated"
	approvalTrustMarkerContents   = "scenery.approval-trust legacy-v1 migration complete\n"
)

type approvalTrustStore struct {
	machine.ArtifactIdentity
	Keys map[string]string `json:"keys"`
}

type legacyApprovalTrustStore struct {
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
	if err := scn.RejectPathSymlinks(root, path); err != nil {
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
	data, err = migrateLegacyApprovalTrustStore(path, data)
	if err != nil {
		return nil, fmt.Errorf("migrate approval trust store: %w", err)
	}
	var store approvalTrustStore
	if err := decodeArtifactExact(data, &store); err != nil {
		return nil, fmt.Errorf("decode approval trust store: %w", err)
	}
	if err := machine.ValidateArtifactIdentity(store.ArtifactIdentity, approvalTrustKind, approvalTrustSchemaDescriptor, "recreate the trust store"); err != nil || len(store.Keys) == 0 {
		return nil, fmt.Errorf("approval trust store has unsupported identity or no keys")
	}
	keys, err := validateApprovalTrustKeys(store.Keys)
	if err != nil {
		return nil, err
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

func migrateLegacyApprovalTrustStore(path string, data []byte) ([]byte, error) {
	markerPath := path + approvalTrustMarkerSuffix
	marker, markerExists, err := readApprovalMigrationFile(markerPath)
	if err != nil {
		return nil, err
	}
	if markerExists {
		if string(marker) != approvalTrustMarkerContents {
			return nil, fmt.Errorf("invalid completion marker")
		}
		return data, nil
	}

	backupPath := path + approvalTrustBackupSuffix
	backup, backupExists, err := readApprovalMigrationFile(backupPath)
	if err != nil {
		return nil, err
	}
	legacyData := data
	if backupExists {
		legacyData = backup
	}
	var legacy legacyApprovalTrustStore
	if err := decodeArtifactExact(legacyData, &legacy); err != nil || legacy.APIVersion != legacyApprovalTrustAPIVersion {
		if backupExists {
			return nil, fmt.Errorf("invalid legacy backup")
		}
		return data, nil
	}
	if len(legacy.Keys) == 0 {
		return nil, fmt.Errorf("legacy trust store has no keys")
	}
	if _, err := validateApprovalTrustKeys(legacy.Keys); err != nil {
		return nil, err
	}

	if !backupExists {
		if err := atomicWriteSynced(backupPath, legacyData, 0o600); err != nil {
			return nil, fmt.Errorf("write owner-only backup: %w", err)
		}
	} else if !bytes.Equal(data, backup) {
		var current approvalTrustStore
		if err := decodeArtifactExact(data, &current); err != nil || machine.ValidateArtifactIdentity(current.ArtifactIdentity, approvalTrustKind, approvalTrustSchemaDescriptor, "rerun the migration") != nil || !maps.Equal(current.Keys, legacy.Keys) {
			return nil, fmt.Errorf("trust store differs from its legacy backup")
		}
		if err := atomicWriteSynced(markerPath, []byte(approvalTrustMarkerContents), 0o600); err != nil {
			return nil, fmt.Errorf("write completion marker: %w", err)
		}
		return data, nil
	}

	migrated, err := json.Marshal(approvalTrustStore{
		ArtifactIdentity: machine.NewArtifactIdentity(approvalTrustKind, approvalTrustSchemaDescriptor),
		Keys:             legacy.Keys,
	})
	if err != nil {
		return nil, err
	}
	migrated = append(migrated, '\n')
	if err := atomicWriteSynced(path, migrated, 0o600); err != nil {
		return nil, fmt.Errorf("write migrated trust store: %w", err)
	}
	if err := atomicWriteSynced(markerPath, []byte(approvalTrustMarkerContents), 0o600); err != nil {
		return nil, fmt.Errorf("write completion marker: %w", err)
	}
	return migrated, nil
}

func readApprovalMigrationFile(path string) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, false, fmt.Errorf("%s must be an owner-only regular non-symlink file", filepath.Base(path))
	}
	data, err := os.ReadFile(path)
	return data, true, err
}

func validateApprovalTrustKeys(values map[string]string) (map[string]ed25519.PublicKey, error) {
	keys := map[string]ed25519.PublicKey{}
	for id, encoded := range values {
		if strings.TrimSpace(id) == "" || strings.Contains(id, ":") || strings.IndexFunc(id, unicode.IsSpace) >= 0 {
			return nil, fmt.Errorf("approval trust store has invalid key id %q", id)
		}
		key, err := decodeApprovalBase64(encoded)
		if err != nil || len(key) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("approval trust store has invalid key %q", id)
		}
		keys[id] = ed25519.PublicKey(append([]byte(nil), key...))
	}
	return keys, nil
}

func decodeApprovalBase64(value string) ([]byte, error) {
	if decoded, err := base64.RawStdEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	return base64.StdEncoding.DecodeString(value)
}
