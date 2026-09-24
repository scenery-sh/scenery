//go:build linux

package appconfig

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
)

// systemdCredentialsBackend encrypts each secret version with systemd-creds
// into a private credential file under secrets/<env>/<key>/<version>.cred.
// Plaintext reaches systemd-creds only on stdin and returns only on stdout.
// A non-root owner uses the per-user credential key (--user).
type systemdCredentialsBackend struct {
	store *Store
	index secretIndex
	user  bool
}

// DefaultSecretBackend returns this host's secret backend for a store.
func DefaultSecretBackend(store *Store) (SecretBackend, error) {
	return &systemdCredentialsBackend{store: store, index: secretIndex{store: store}, user: os.Geteuid() != 0}, nil
}

func (b *systemdCredentialsBackend) Name() string { return "systemd-creds" }

func (b *systemdCredentialsBackend) args(action string, rest ...string) []string {
	args := []string{}
	if b.user {
		args = append(args, "--user")
	}
	return append(append(args, action), rest...)
}

func (b *systemdCredentialsBackend) Ready(ctx context.Context) error {
	path, err := exec.LookPath("systemd-creds")
	if err != nil {
		return fmt.Errorf("%w: systemd-creds is not installed", ErrSecretBackendUnavailable)
	}
	command := exec.CommandContext(ctx, path, b.args("encrypt", "--name=scenery-readiness", "-", "-")...)
	command.Stdin = bytes.NewReader([]byte("readiness"))
	if err := command.Run(); err != nil {
		return fmt.Errorf("%w: systemd-creds cannot encrypt for this owner (%s); systemd 256 or newer is required for per-user credentials", ErrSecretBackendUnavailable, redactedSubprocessError("systemd-creds", "encrypt", err))
	}
	return nil
}

func (b *systemdCredentialsBackend) Create(ctx context.Context, environment, key string, value []byte) (SecretVersion, error) {
	if err := checkSecretInput(environment, key, value); err != nil {
		return SecretVersion{}, err
	}
	version, err := NewSecretVersion(b.Name())
	if err != nil {
		return SecretVersion{}, err
	}
	name := credentialName(b.store.appID, environment, key, version.Version)
	var encrypted bytes.Buffer
	command := exec.CommandContext(ctx, "systemd-creds", b.args("encrypt", "--name="+name, "-", "-")...)
	command.Stdin, command.Stdout = bytes.NewReader(value), &encrypted
	if err := command.Run(); err != nil {
		return SecretVersion{}, redactedSubprocessError("systemd-creds", "encrypt", err)
	}
	if err := b.index.add(ctx, environment, key, version.Version); err != nil {
		return SecretVersion{}, err
	}
	if err := b.store.writePrivateCredential(environment, key, version.Version, encrypted.Bytes()); err != nil {
		return SecretVersion{}, err
	}
	return version, nil
}

func (b *systemdCredentialsBackend) Resolve(ctx context.Context, environment, key string, version SecretVersion) ([]byte, error) {
	if err := checkVersion(b.Name(), version); err != nil {
		return nil, err
	}
	encrypted, err := b.store.readPrivateCredential(environment, key, version.Version)
	if err != nil {
		return nil, fmt.Errorf("%w: secret %s version is not retained: %v", ErrSecretBackendUnavailable, key, err)
	}
	name := credentialName(b.store.appID, environment, key, version.Version)
	var plaintext bytes.Buffer
	command := exec.CommandContext(ctx, "systemd-creds", b.args("decrypt", "--name="+name, "-", "-")...)
	command.Stdin, command.Stdout = bytes.NewReader(encrypted), &plaintext
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrSecretBackendUnavailable, redactedSubprocessError("systemd-creds", "decrypt", err))
	}
	return plaintext.Bytes(), nil
}

func (b *systemdCredentialsBackend) Remove(ctx context.Context, environment, key string, version SecretVersion) error {
	if err := checkVersion(b.Name(), version); err != nil {
		return err
	}
	if err := b.store.removePrivateCredential(environment, key, version.Version); err != nil {
		return err
	}
	return b.index.remove(ctx, environment, key, version.Version)
}

func (b *systemdCredentialsBackend) Versions(_ context.Context, environment string) (map[string][]SecretVersion, error) {
	return b.index.versions(b.Name(), environment)
}

// credentialName is the stable backend-side name of one secret version.
func credentialName(appID, environment, key, version string) string {
	return appID + "." + environment + "." + key + "." + version
}

// writePrivateCredential writes a backend-encrypted credential file.
func (s *Store) writePrivateCredential(environment, key, version string, data []byte) error {
	root, err := s.root(true)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	directory := path.Join("secrets", environment, key)
	if err := mkdirs(root, directory); err != nil {
		return err
	}
	return s.writeAtomic(root, path.Join(directory, version+".cred"), data)
}

func (s *Store) readPrivateCredential(environment, key, version string) ([]byte, error) {
	root, err := s.root(false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return readRegular(root, path.Join("secrets", environment, key, version+".cred"), 4*MaxSecretBytes)
}

func (s *Store) removePrivateCredential(environment, key, version string) error {
	root, err := s.root(false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	err = root.Remove(path.Join("secrets", environment, key, version+".cred"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
