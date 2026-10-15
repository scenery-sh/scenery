package appconfig

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"strings"
)

var ErrConnectionCredentialConflict = errors.New("connection credentials are already configured differently; no existing value was overwritten")
var ErrConnectionCredentialSource = errors.New("existing Google client credentials are required for connection migration")

// MigrateGoogleConnectionCredentials copies two legacy environment inputs into
// the google connection slot, preserving sign-in inputs and the cipher key.
// Each published revision changes one key. Rerun safely resumes a partial copy;
// a stale revision or conflicting destination never overwrites stored data.
// Plaintext crosses only memory and the existing OS secret backend.
func (s *Store) MigrateGoogleConnectionCredentials(ctx context.Context, environment, expected string, backend SecretBackend) (MutationResult, error) {
	document, _, err := s.Read(environment)
	if err != nil {
		return MutationResult{}, err
	}
	result := MutationResult{PreviousRevision: document.Revision, Revision: document.Revision}
	if expected != "" && expected != document.Revision {
		return result, ErrRevisionConflict
	}
	const oldID = "auth.google_client_id"
	const oldSecret = "auth.google_client_secret"
	const newID = "auth.connections.google.client_id"
	const newSecret = "auth.connections.google.client_secret"
	value, ok := document.Values[oldID]
	version, secretOK := document.Secrets[oldSecret]
	var clientID string
	if !ok || !secretOK || json.Unmarshal(value, &clientID) != nil || strings.TrimSpace(clientID) == "" {
		return result, ErrConnectionCredentialSource
	}
	if current, exists := document.Values[newID]; exists && !bytes.Equal(current, value) {
		return result, ErrConnectionCredentialConflict
	}
	if _, exists := document.Secrets[newID]; exists {
		return result, ErrConnectionCredentialConflict
	}
	if _, exists := document.Values[newSecret]; exists {
		return result, ErrConnectionCredentialConflict
	}
	if backend == nil || backend.Name() != version.Backend {
		return result, ErrSecretBackendUnavailable
	}
	if err := backend.Ready(ctx); err != nil {
		return result, ErrSecretBackendUnavailable
	}
	plaintext, err := backend.Resolve(ctx, environment, oldSecret, version)
	if err != nil {
		return result, ErrSecretBackendUnavailable
	}
	defer clear(plaintext)
	if len(plaintext) == 0 || len(plaintext) > MaxSecretBytes {
		return result, ErrConnectionCredentialSource
	}
	if current, exists := document.Secrets[newSecret]; exists {
		if current.Backend != backend.Name() {
			return result, ErrConnectionCredentialConflict
		}
		other, err := backend.Resolve(ctx, environment, newSecret, current)
		if err != nil {
			return result, ErrSecretBackendUnavailable
		}
		same := subtle.ConstantTimeCompare(plaintext, other) == 1
		clear(other)
		if !same {
			return result, ErrConnectionCredentialConflict
		}
	}
	if _, exists := document.Values[newID]; !exists {
		change, err := s.Mutate(ctx, environment, Mutation{Key: newID, Value: value, ExpectRevision: result.Revision})
		if err != nil {
			return result, err
		}
		result.Changed = result.Changed || change.Changed
		result.Revision = change.Revision
	}
	if _, exists := document.Secrets[newSecret]; !exists {
		copy, err := backend.Create(ctx, environment, newSecret, plaintext)
		if err != nil {
			return result, ErrSecretBackendUnavailable
		}
		change, err := s.Mutate(ctx, environment, Mutation{Key: newSecret, Secret: &copy, ExpectRevision: result.Revision})
		if err != nil {
			return result, err
		}
		result.Changed = result.Changed || change.Changed
		result.Revision = change.Revision
	}
	return result, nil
}
