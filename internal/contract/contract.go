package contract

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"time"

	"scenery.sh/internal/runtimeapi"
)

// Int is the arbitrary-precision integer used by Scenery contracts.
type Int struct{ big.Int }

// Decimal preserves an exact coefficient and base-10 scale.
type Decimal struct {
	Coefficient big.Int
	Scale       int32
}

type UUID string
type Date string
type DateTime time.Time
type Duration struct{ nanoseconds big.Int }
type Size struct{ bytes big.Int }
type URL url.URL
type RelativePath string

// HostPath is an absolute path on the execution target. It is a
// deployment-only scalar: it may type package inputs that deployments bind,
// never wire contracts.
type HostPath string
type JSON = json.RawMessage

// Unit is the canonical value for a contract with no semantic fields.
type Unit struct{}

// Optional distinguishes an absent field from a present zero value.
type Optional[T any] struct {
	Value T
	Set   bool
}

func Some[T any](value T) Optional[T] { return Optional[T]{Value: value, Set: true} }
func NoneOf[T any]() Optional[T]      { return Optional[T]{} }

// Nullable distinguishes a present null from a present concrete value.
type Nullable[T any] struct {
	Value T
	Null  bool
}

func ValueOf[T any](value T) Nullable[T] { return Nullable[T]{Value: value} }
func NullOf[T any]() Nullable[T]         { return Nullable[T]{Null: true} }

// Set is represented canonically by generator/runtime adapters.
type Set[T any] []T

type SecretRef struct {
	Address string
}

var secretRevealer func(SecretRef) ([]byte, bool, error)

// SetSecretRevealer installs the runtime's secret resolution. Only the
// runtime calls it.
func SetSecretRevealer(reveal func(SecretRef) ([]byte, bool, error)) { secretRevealer = reveal }

// Reveal returns the plaintext of a secret the selected environment
// configured. The bytes belong to the caller; never log or return them.
func (ref SecretRef) Reveal() ([]byte, error) {
	if secretRevealer == nil {
		return nil, fmt.Errorf("secret %s cannot be revealed outside a Scenery runtime", ref.Address)
	}
	value, ok, err := secretRevealer(ref)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("secret %s is not configured for this environment", ref.Address)
	}
	return value, nil
}

type ExecutionReceipt = runtimeapi.ExecutionReceipt

type Problem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
}

type Invocation = runtimeapi.Invocation

func InvocationFromContext(ctx context.Context) (Invocation, bool) {
	return runtimeapi.InvocationFromContext(ctx)
}

// Registry is the generated-adapter registration boundary.
type Registry = runtimeapi.Registry
