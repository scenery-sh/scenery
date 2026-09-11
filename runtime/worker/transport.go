package worker

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"scenery.sh/internal/nativeprotocol"
	"slices"
	"time"
)

const Protocol = nativeprotocol.Protocol
const maxMessageBytes = nativeprotocol.MaxMessageBytes

type InvocationRequest = nativeprotocol.InvocationRequest
type InvocationResponse = nativeprotocol.InvocationResponse

// Admission is compiled into the worker. The first experiment admits only
// explicitly selected unary bindings; other native callbacks remain linked.
type Admission struct {
	Operation string
	Binding   string
}

type Handler struct {
	registry    *Registry
	token       string
	contract    string
	inputDigest string
	admissions  map[string]string
}

func NewHandler(registry *Registry, token, contract, inputDigest string, admissions []Admission) (*Handler, error) {
	if registry == nil || len(token) < 32 || contract == "" || inputDigest == "" {
		return nil, fmt.Errorf("native worker transport requires registry, private token and compiled identities")
	}
	registry.mu.RLock()
	sealed := registry.sealed
	registry.mu.RUnlock()
	if !sealed {
		return nil, fmt.Errorf("native worker transport requires a sealed composition")
	}
	handler := &Handler{registry: registry, token: token, contract: contract, inputDigest: inputDigest, admissions: map[string]string{}}
	for _, admission := range admissions {
		operation, exists := registry.operation(admission.Operation)
		if !exists || operation.Streaming || admission.Binding == "" {
			return nil, fmt.Errorf("native worker binding %s is not an admitted unary operation", admission.Binding)
		}
		if _, exists := handler.admissions[admission.Operation]; exists {
			return nil, fmt.Errorf("duplicate worker admission %s", admission.Operation)
		}
		handler.admissions[admission.Operation] = admission.Binding
	}
	if len(handler.admissions) == 0 {
		return nil, fmt.Errorf("native worker requires at least one admitted operation")
	}
	return handler, nil
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/invoke" || request.Method != http.MethodPost {
		http.NotFound(writer, request)
		return
	}
	if subtle.ConstantTimeCompare([]byte(request.Header.Get("Authorization")), []byte("Bearer "+handler.token)) != 1 {
		http.Error(writer, "private worker channel required", http.StatusUnauthorized)
		return
	}
	var invocation InvocationRequest
	if err := decodeOne(http.MaxBytesReader(writer, request.Body, maxMessageBytes), &invocation); err != nil {
		http.Error(writer, "invalid worker request", http.StatusBadRequest)
		return
	}
	if invocation.Protocol != Protocol || invocation.ProtocolRevision != nativeprotocol.Revision || invocation.ContractRevision != handler.contract || invocation.InputDigest != handler.inputDigest {
		http.Error(writer, "worker identity mismatch", http.StatusConflict)
		return
	}
	binding, admitted := handler.admissions[invocation.Operation]
	if !admitted || binding != invocation.Context.CallerBinding {
		http.Error(writer, "operation is not admitted by this experiment", http.StatusNotImplemented)
		return
	}
	response, err := handler.invoke(request.Context(), invocation)
	if err != nil {
		// Native errors remain intact until this explicit process boundary. The
		// kernel receives a bounded classification, never a concrete Go error.
		response = InvocationResponse{Error: "native_execution_failed"}
	}
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(response); err != nil {
		return
	}
}

func (handler *Handler) invoke(ctx context.Context, invocation InvocationRequest) (response InvocationResponse, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			response = InvocationResponse{}
			err = fmt.Errorf("native handler panicked")
		}
	}()
	if !invocation.Context.Deadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, invocation.Context.Deadline)
		defer cancel()
	}
	if err := ctx.Err(); err != nil {
		return response, err
	}
	operation, exists := handler.registry.operation(invocation.Operation)
	if !exists {
		return response, fmt.Errorf("native operation is not registered")
	}
	input, err := operation.Decode(invocation.Input)
	if err != nil {
		return response, err
	}
	ctx, state, leave, err := enterRequest(ctx, invocation.Context, input)
	if err != nil {
		return response, err
	}
	defer leave()
	outcome, stream, err := operation.Invoke(ctx, input)
	if err != nil {
		_ = stream.Close()
		return response, err
	}
	if operation.Streaming {
		_ = stream.Close()
		return response, fmt.Errorf("stream transport is not implemented")
	}
	if outcome == nil {
		return response, fmt.Errorf("native handler returned no outcome")
	}
	encoded, err := operation.Encode(outcome)
	if err != nil {
		return response, err
	}
	if len(encoded) > maxMessageBytes {
		return response, fmt.Errorf("native outcome exceeds the experimental transport limit")
	}
	state.mu.Lock()
	spans := slices.Clone(state.spans)
	state.mu.Unlock()
	return InvocationResponse{Outcome: encoded, Spans: spans}, nil
}

func decodeOne(reader io.Reader, value any) error {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("expected exactly one JSON message")
	}
	return nil
}

// Drain first joins HTTP calls, then shuts down native services. A failed drain
// must not be interpreted by the owner as permission to activate another writer.
func Drain(server *http.Server, registry *Registry) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		return err
	}
	return registry.services.Shutdown(ctx)
}
