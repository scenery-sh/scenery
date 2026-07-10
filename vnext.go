package scenery

import (
	"encoding/json"
	"math/big"
	"net/url"
	"time"
)

// Int is the arbitrary-precision integer used by edition-2027 contracts.
type Int struct{ big.Int }

// Decimal preserves an exact coefficient and base-10 scale.
type Decimal struct {
	Coefficient big.Int
	Scale       int32
}

type UUID string
type Date string
type DateTime time.Time
type Duration time.Duration
type Size uint64
type URL url.URL
type RelativePath string
type JSON = json.RawMessage

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

type Problem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
}

type Invocation struct {
	ID       UUID
	TenantID Optional[string]
	TraceID  Optional[string]
	Deadline Optional[DateTime]
}

// Registry is the generated-adapter registration boundary.
type Registry interface {
	Register(address string, implementation any) error
}
