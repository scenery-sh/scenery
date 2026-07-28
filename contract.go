package scenery

import (
	"context"
	"encoding/json"
	"time"

	"scenery.sh/internal/contract"
	"scenery.sh/internal/runtimeapi"
)

// The contract surface is implemented in scenery.sh/internal/contract so that
// the compiler, source, and generator packages can depend on contract values
// without linking the app runtime. This file is the app-facing spelling: it is
// the only name generated app code uses.

// Int is the arbitrary-precision integer used by Scenery contracts.
type Int = contract.Int

// Decimal preserves an exact coefficient and base-10 scale.
type Decimal = contract.Decimal

type UUID = contract.UUID
type Date = contract.Date
type DateTime = contract.DateTime
type Duration = contract.Duration
type Size = contract.Size
type URL = contract.URL
type RelativePath = contract.RelativePath
type JSON = json.RawMessage

// Unit is the canonical value for a contract with no semantic fields.
type Unit = contract.Unit

// Optional distinguishes an absent field from a present zero value.
type Optional[T any] = contract.Optional[T]

func Some[T any](value T) Optional[T] { return contract.Some(value) }
func NoneOf[T any]() Optional[T]      { return contract.NoneOf[T]() }

// Nullable distinguishes a present null from a present concrete value.
type Nullable[T any] = contract.Nullable[T]

func ValueOf[T any](value T) Nullable[T] { return contract.ValueOf(value) }
func NullOf[T any]() Nullable[T]         { return contract.NullOf[T]() }

// Set is represented canonically by generator/runtime adapters.
type Set[T any] = contract.Set[T]

type SecretRef = contract.SecretRef

type ExecutionReceipt = runtimeapi.ExecutionReceipt

type Problem = contract.Problem

type Invocation = runtimeapi.Invocation

func InvocationFromContext(ctx context.Context) (Invocation, bool) {
	return contract.InvocationFromContext(ctx)
}

// Registry is the generated-adapter registration boundary.
type Registry = runtimeapi.Registry

// ContractConstraints is emitted by generated contract packages. Pointer
// fields distinguish an absent limit from an explicit zero limit.
type ContractConstraints = contract.ContractConstraints

// ContractNamedDecoder lets a generated package provide codecs for named
// tagged unions while the shared runtime continues to recurse through
// optional, nullable, collection, map, and tuple wrappers.
type ContractNamedDecoder = contract.ContractNamedDecoder

// ContractValidationError is a declared record-validation failure. Code,
// message, and path come from the named validation block in the contract.
type ContractValidationError = contract.ContractValidationError

func ParseInt(value string) (Int, error)                   { return contract.ParseInt(value) }
func ParseDecimal(value string) (Decimal, error)           { return contract.ParseDecimal(value) }
func ParseUUID(value string) (UUID, error)                 { return contract.ParseUUID(value) }
func ParseDate(value string) (Date, error)                 { return contract.ParseDate(value) }
func ParseDateTime(value string) (DateTime, error)         { return contract.ParseDateTime(value) }
func ParseDuration(value string) (Duration, error)         { return contract.ParseDuration(value) }
func ParseSize(value string) (Size, error)                 { return contract.ParseSize(value) }
func ParseURL(value string) (URL, error)                   { return contract.ParseURL(value) }
func ParseRelativePath(value string) (RelativePath, error) { return contract.ParseRelativePath(value) }

func ContractIntConstraint(value int64) *int64      { return contract.ContractIntConstraint(value) }
func ContractStringConstraint(value string) *string { return contract.ContractStringConstraint(value) }

// DecodeJSONObject is the strict object decoder used by generated contract
// records.
func DecodeJSONObject(data []byte) (map[string]json.RawMessage, error) {
	return contract.DecodeJSONObject(data)
}

// DecodeContractOutcomeEnvelope validates a closed durable outcome envelope
// and returns a defensive copy of its schema-directed payload.
func DecodeContractOutcomeEnvelope(data []byte) (kind, name string, payload json.RawMessage, err error) {
	return contract.DecodeContractOutcomeEnvelope(data)
}

// MarshalContractOutcomeVariant encodes one closed operation outcome using a
// stable envelope suitable for durable storage and wait delivery.
func MarshalContractOutcomeVariant(kind, name string, value any, typeExpression string) ([]byte, error) {
	return contract.MarshalContractOutcomeVariant(kind, name, value, typeExpression)
}

func EncodeContractCompositeKey(components ...[]byte) (string, error) {
	return contract.EncodeContractCompositeKey(components...)
}

// EncodeContractKeyComponent preserves the schema type and presence state of
// one idempotency or concurrency-key component.
func EncodeContractKeyComponent(value any, typeExpression string) ([]byte, error) {
	return contract.EncodeContractKeyComponent(value, typeExpression)
}

// MarshalContractValue applies the schema-directed contract JSON wire
// representation. Generated contract packages use it for record fields so
// exact integers, nested collections, and sets never fall back to Go's
// lossy/default encoding choices.
func MarshalContractValue(value any, typeExpression string) ([]byte, error) {
	return contract.MarshalContractValue(value, typeExpression)
}

// UnmarshalContractValue decodes one schema-directed contract value into
// target. Target must be a non-nil pointer.
func UnmarshalContractValue(data []byte, target any, typeExpression string) error {
	return contract.UnmarshalContractValue(data, target, typeExpression)
}

func UnmarshalContractValueWithNamed(data []byte, target any, typeExpression string, named ContractNamedDecoder) error {
	return contract.UnmarshalContractValueWithNamed(data, target, typeExpression, named)
}

// ValidateContractValue enforces one field's contract constraints against
// the generated Go representation.
func ValidateContractValue(value any, typeExpression string, constraints ContractConstraints) error {
	return contract.ValidateContractValue(value, typeExpression, constraints)
}

// ValidateContractRecord evaluates a compiler-validated, data-only expression
// over generated record fields. The public runtime deliberately does not
// include the HCL compiler; generated packages carry the compiled expression.
func ValidateContractRecord(fields map[string]any, fieldTypes map[string]string, encodedProgram, code, message, path string) error {
	return contract.ValidateContractRecord(fields, fieldTypes, encodedProgram, code, message, path)
}

// ApprovalToken is a detached, plan-bound authorization for explicitly named
// risk scopes. Signature is excluded from ApprovalTokenPayload.
type ApprovalToken = contract.ApprovalToken

// NewApprovalToken creates the current detached approval shape. The caller
// signs ApprovalTokenPayload and then fills Signature.
func NewApprovalToken(planID, caller string, riskScopes []string, expiresAt time.Time) ApprovalToken {
	return contract.NewApprovalToken(planID, caller, riskScopes, expiresAt)
}

// ApprovalTokenPayload returns the canonical bytes an approval service signs.
func ApprovalTokenPayload(token ApprovalToken) ([]byte, error) {
	return contract.ApprovalTokenPayload(token)
}

// ValidateApprovalToken enforces the current scenery.approval-token shape and
// signature encoding before a caller attempts trust-root verification.
func ValidateApprovalToken(token ApprovalToken) error {
	return contract.ValidateApprovalToken(token)
}
