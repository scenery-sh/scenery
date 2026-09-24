package appconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
)

// ErrSecretBackendUnavailable reports that this host has no usable secret
// backend for environment configuration. Secret-free workflows keep working.
var ErrSecretBackendUnavailable = errors.New("secret storage is unavailable")

// SecretBackend stores immutable secret versions. Plaintext crosses only
// in-memory buffers and subprocess stdin/stdout pipes, never arguments, logs
// or the environment document.
type SecretBackend interface {
	// Name is the backend identifier recorded in secret version references.
	Name() string
	// Ready fails with a specific reason when the backend cannot be used
	// non-interactively by this process owner.
	Ready(ctx context.Context) error
	// Create stores a new immutable version of key.
	Create(ctx context.Context, environment, key string, value []byte) (SecretVersion, error)
	// Resolve returns the plaintext of one version.
	Resolve(ctx context.Context, environment, key string, version SecretVersion) ([]byte, error)
	// Remove deletes one version that no retained revision references.
	Remove(ctx context.Context, environment, key string, version SecretVersion) error
	// Versions lists the versions the backend holds, by key.
	Versions(ctx context.Context, environment string) (map[string][]SecretVersion, error)
}

// secretIndex records which versions a backend holds. It never contains
// secret material; it lets pruning find unreferenced versions.
type secretIndex struct {
	store *Store
}

func (index secretIndex) path(environment string) string {
	return path.Join("secrets", environment, "index.json")
}

func (index secretIndex) read(root *os.Root, environment string) (map[string][]string, error) {
	data, err := readRegular(root, index.path(environment), MaxDocumentBytes)
	if errors.Is(err, os.ErrNotExist) {
		return map[string][]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	versions := map[string][]string{}
	if err := json.Unmarshal(data, &versions); err != nil {
		return nil, fmt.Errorf("secret index of %s is malformed: %w", environment, err)
	}
	for key, list := range versions {
		for _, version := range list {
			if !keyPattern.MatchString(key) || !versionPattern.MatchString(version) {
				return nil, fmt.Errorf("secret index of %s is malformed", environment)
			}
		}
	}
	return versions, nil
}

// update changes the index under the environment lock.
func (index secretIndex) update(ctx context.Context, environment string, change func(map[string][]string)) error {
	root, err := index.store.root(true)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	unlock, err := lockEnvironment(ctx, root, environment)
	if err != nil {
		return err
	}
	defer unlock()
	versions, err := index.read(root, environment)
	if err != nil {
		return err
	}
	change(versions)
	for key, list := range versions {
		if len(list) == 0 {
			delete(versions, key)
			continue
		}
		sort.Strings(list)
	}
	encoded, err := json.MarshalIndent(versions, "", "  ")
	if err != nil {
		return err
	}
	if err := mkdirs(root, path.Join("secrets", environment)); err != nil {
		return err
	}
	return index.store.writeAtomic(root, index.path(environment), append(encoded, '\n'))
}

func (index secretIndex) add(ctx context.Context, environment, key, version string) error {
	return index.update(ctx, environment, func(versions map[string][]string) {
		versions[key] = append(versions[key], version)
	})
}

func (index secretIndex) remove(ctx context.Context, environment, key, version string) error {
	return index.update(ctx, environment, func(versions map[string][]string) {
		kept := versions[key][:0]
		for _, existing := range versions[key] {
			if existing != version {
				kept = append(kept, existing)
			}
		}
		versions[key] = kept
	})
}

func (index secretIndex) versions(backend, environment string) (map[string][]SecretVersion, error) {
	root, err := index.store.root(false)
	if errors.Is(err, os.ErrNotExist) {
		return map[string][]SecretVersion{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	raw, err := index.read(root, environment)
	if err != nil {
		return nil, err
	}
	result := map[string][]SecretVersion{}
	for key, list := range raw {
		for _, version := range list {
			result[key] = append(result[key], SecretVersion{Backend: backend, Version: version})
		}
	}
	return result, nil
}

// checkSecretInput validates a secret mutation's request identity.
func checkSecretInput(environment, key string, value []byte) error {
	if err := checkEnvironment(environment); err != nil {
		return err
	}
	if !keyPattern.MatchString(key) {
		return fmt.Errorf("configuration key %q is malformed", key)
	}
	if len(value) == 0 {
		return fmt.Errorf("%s: an empty secret is not a value; use unset", key)
	}
	if len(value) > MaxSecretBytes {
		return fmt.Errorf("%s: secret exceeds %d bytes", key, MaxSecretBytes)
	}
	return nil
}

func checkVersion(backend string, version SecretVersion) error {
	if version.Backend != backend || !versionPattern.MatchString(version.Version) {
		return fmt.Errorf("secret version belongs to backend %q, not %q", version.Backend, backend)
	}
	return nil
}

// redactedSubprocessError turns a backend tool failure into an error without
// its output, which could echo request material.
func redactedSubprocessError(tool, action string, err error) error {
	message := "failed"
	var exit interface{ ExitCode() int }
	if errors.As(err, &exit) {
		message = fmt.Sprintf("exited with status %d", exit.ExitCode())
	}
	return fmt.Errorf("%s %s %s", tool, strings.TrimSpace(action), message)
}
