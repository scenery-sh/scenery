//go:build !darwin && !linux

package appconfig

import (
	"fmt"
	"runtime"
)

// DefaultSecretBackend reports that this platform has no supported backend.
func DefaultSecretBackend(*Store) (SecretBackend, error) {
	return nil, fmt.Errorf("%w on %s", ErrSecretBackendUnavailable, runtime.GOOS)
}
