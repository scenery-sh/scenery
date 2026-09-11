// Package runtime supplies native application calls. The generated composition
// explicitly imports runtime/host to install HTTP and framework lifecycle owners.
// Importing this package alone does not link the host into an application worker.
package runtime

import (
	"context"
	"time"

	"scenery.sh/internal/nativecall"
	"scenery.sh/internal/nativedurable"
	"scenery.sh/internal/runtimeapi"
	"scenery.sh/internal/runtimeapp"
	"scenery.sh/runtime/shared"
)

type AuthInfo = runtimeapp.AuthInfo
type Span = runtimeapp.Span
type ContractByteStream = runtimeapp.ByteStream
type DurableRun = nativedurable.Run
type DurableStartRequest = nativedurable.StartRequest
type DurableExecutionFailure = nativedurable.ExecutionFailure
type ContractDurableDispatchOptions = nativedurable.DispatchOptions

func Meta() *shared.AppMetadata       { return runtimeapp.Meta() }
func CurrentRequest() *shared.Request { return runtimeapp.CurrentRequest() }
func CurrentAuth() *AuthInfo          { return runtimeapp.CurrentAuth() }
func WithAuthContext(ctx context.Context, auth AuthInfo) context.Context {
	return runtimeapp.WithAuthContext(ctx, auth)
}
func StartSpan(ctx context.Context, name string) (context.Context, *Span) {
	return runtimeapp.StartSpan(ctx, name)
}
func LoadDotEnvIntoEnv() error { return runtimeapp.LoadDotEnvIntoEnv() }
func TraceDBQueryStart(ctx context.Context, query string, argsCount int) context.Context {
	return runtimeapp.TraceDBQueryStart(ctx, query, argsCount)
}
func TraceDBQueryEnd(ctx context.Context, commandTag string, rowsAffected int64, err error) {
	runtimeapp.TraceDBQueryEnd(ctx, commandTag, rowsAffected, err)
}

func InvokeContractBindingJSON(ctx context.Context, address, callerPackage string, input []byte) ([]byte, error) {
	return nativecall.Current().InvokeJSON(ctx, address, callerPackage, input)
}
func InvokeContractBinding(ctx context.Context, address string, invocation, input any) (any, error) {
	return InvokeContractBindingFrom(ctx, address, "", invocation, input)
}
func InvokeContractBindingFrom(ctx context.Context, address, callerPackage string, invocation, input any) (any, error) {
	return nativecall.Current().InvokeFrom(ctx, address, callerPackage, invocation, input)
}

func StartDurableTask(ctx context.Context, request DurableStartRequest) (DurableRun, error) {
	return nativedurable.Start(ctx, request)
}
func WaitDurableTask(ctx context.Context, run DurableRun) ([]byte, error) {
	return nativedurable.Wait(ctx, run)
}
func DurableSignal(ctx context.Context, service, jobID, name, dedupeKey string, payload []byte) error {
	return nativedurable.Signal(ctx, service, jobID, name, dedupeKey, payload)
}
func DurableSchedule(ctx context.Context, service, taskName, id string, every time.Duration, input []byte) error {
	return nativedurable.Schedule(ctx, service, taskName, id, every, input)
}
func DurableStep(ctx context.Context, key string, run func(context.Context) ([]byte, error)) ([]byte, error) {
	return nativedurable.Step(ctx, key, run)
}
func DispatchContractDurableExecutionWithOptions(ctx context.Context, address string, input any, options ContractDurableDispatchOptions) (runtimeapi.ExecutionReceipt, error) {
	return nativedurable.Dispatch(ctx, address, input, options)
}
func DispatchAndWaitContractDurableExecutionWithOptions(ctx context.Context, address string, input any, options ContractDurableDispatchOptions) ([]byte, error) {
	return nativedurable.DispatchAndWait(ctx, address, input, options)
}
func ContractDurableFailureOutcome(err error) string { return nativedurable.FailureOutcome(err) }
