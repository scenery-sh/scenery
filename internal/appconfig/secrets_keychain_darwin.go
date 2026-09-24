//go:build darwin

package appconfig

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

const keychainTool = "/usr/bin/security"

// keychainBackend stores each secret version as one generic password in the
// invoking user's default keychain. Plaintext reaches security(1) only as
// hex data on the stdin of its interactive mode; reads come back over stdout.
type keychainBackend struct {
	store *Store
	index secretIndex
}

// DefaultSecretBackend returns this host's secret backend for a store.
func DefaultSecretBackend(store *Store) (SecretBackend, error) {
	return &keychainBackend{store: store, index: secretIndex{store: store}}, nil
}

func (b *keychainBackend) Name() string { return "keychain" }

func (b *keychainBackend) service(environment string) string {
	return "sh.scenery.config." + b.store.appID + "." + environment
}

func (b *keychainBackend) Ready(ctx context.Context) error {
	command := exec.CommandContext(ctx, keychainTool, "show-keychain-info")
	command.Stdout, command.Stderr = nil, nil
	if err := command.Run(); err != nil {
		return fmt.Errorf("%w: the default macOS keychain is locked or unavailable to this process (%s); unlock it or run from the owning user session", ErrSecretBackendUnavailable, redactedSubprocessError("security", "show-keychain-info", err))
	}
	return nil
}

func (b *keychainBackend) Create(ctx context.Context, environment, key string, value []byte) (SecretVersion, error) {
	if err := checkSecretInput(environment, key, value); err != nil {
		return SecretVersion{}, err
	}
	version, err := NewSecretVersion(b.Name())
	if err != nil {
		return SecretVersion{}, err
	}
	// The index names the version before the keychain holds it, so an
	// interrupted creation leaves at most an indexed orphan that pruning
	// removes, never an unindexed keychain item.
	if err := b.index.add(ctx, environment, key, version.Version); err != nil {
		return SecretVersion{}, err
	}
	encoded := hex.EncodeToString([]byte(base64.StdEncoding.EncodeToString(value)))
	request := fmt.Sprintf("add-generic-password -a %q -s %q -l %q -X %s\n", key+"/"+version.Version, b.service(environment), "Scenery configuration "+key, encoded)
	command := exec.CommandContext(ctx, keychainTool, "-i")
	command.Stdin = strings.NewReader(request)
	if err := command.Run(); err != nil {
		return SecretVersion{}, redactedSubprocessError("security", "add-generic-password", err)
	}
	return version, nil
}

func (b *keychainBackend) Resolve(ctx context.Context, environment, key string, version SecretVersion) ([]byte, error) {
	if err := checkVersion(b.Name(), version); err != nil {
		return nil, err
	}
	var stdout bytes.Buffer
	command := exec.CommandContext(ctx, keychainTool, "find-generic-password", "-a", key+"/"+version.Version, "-s", b.service(environment), "-w")
	command.Stdout = &stdout
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("%w: secret %s version is not readable: %s", ErrSecretBackendUnavailable, key, redactedSubprocessError("security", "find-generic-password", err))
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(stdout.String()))
	if err != nil {
		return nil, fmt.Errorf("secret %s version is not a Scenery keychain item", key)
	}
	return decoded, nil
}

func (b *keychainBackend) Remove(ctx context.Context, environment, key string, version SecretVersion) error {
	if err := checkVersion(b.Name(), version); err != nil {
		return err
	}
	command := exec.CommandContext(ctx, keychainTool, "delete-generic-password", "-a", key+"/"+version.Version, "-s", b.service(environment))
	if err := command.Run(); err != nil {
		var exit interface{ ExitCode() int }
		// 44 is errSecItemNotFound: the version is already gone.
		if !errors.As(err, &exit) || exit.ExitCode() != 44 {
			return redactedSubprocessError("security", "delete-generic-password", err)
		}
	}
	return b.index.remove(ctx, environment, key, version.Version)
}

func (b *keychainBackend) Versions(_ context.Context, environment string) (map[string][]SecretVersion, error) {
	return b.index.versions(b.Name(), environment)
}
