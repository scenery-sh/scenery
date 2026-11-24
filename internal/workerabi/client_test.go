package workerabi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClientClassifiesDeliveryAndAttestsIdentity(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "worker-abi-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socket := filepath.Join(directory, "worker.sock")
	identity := Identity{ABI: Version, Service: "quote/service/api", ContractRevision: "revision-one", ImplementationRevision: "implementation-one"}
	client := NewClient(socket, "private-test-token", identity)
	t.Cleanup(client.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	invocation := Invocation{Service: identity.Service, Operation: "quote/operation/price", Mode: "direct", Input: json.RawMessage("{}")}
	_, err = client.Invoke(ctx, invocation)
	if failure, ok := errors.AsType[*DeliveryError](err); !ok || failure.Delivery != NotSent {
		t.Fatalf("dial failure = %v, want not_sent", err)
	}

	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 1)
	canceled := make(chan struct{}, 1)
	server := &http.Server{Protocols: Protocols(), ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(ContractHeader, identity.ContractRevision)
		w.Header().Set(ImplementationHeader, identity.ImplementationRevision)
		switch r.URL.Path {
		case ReadyPath:
			_ = json.NewEncoder(w).Encode(identity)
		case InvokePath:
			var call Invocation
			if err := json.NewDecoder(r.Body).Decode(&call); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if call.Context.ContractRevision == "conflict" {
				w.WriteHeader(http.StatusConflict)
				return
			}
			if call.Context.ContractRevision == "cancel" {
				entered <- struct{}{}
				<-r.Context().Done()
				canceled <- struct{}{}
				return
			}
			panic(http.ErrAbortHandler)
		}
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	if err := client.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	wrongIdentity := identity
	wrongIdentity.ContractRevision = "wrong-revision"
	wrongClient := NewClient(socket, "private-test-token", wrongIdentity)
	t.Cleanup(wrongClient.Close)
	if err := wrongClient.Ready(ctx); err == nil {
		t.Fatal("accepted a mismatched contract revision")
	}
	if _, err = client.Invoke(ctx, invocation); err == nil {
		t.Fatal("accepted an invocation without an attested reply")
	} else if failure, ok := errors.AsType[*DeliveryError](err); !ok || failure.Delivery != Unknown {
		t.Fatalf("stream aborted after decode = %v, want unknown", err)
	}
	invocation.Context.ContractRevision = "conflict"
	if _, err := client.Invoke(ctx, invocation); err == nil {
		t.Fatal("accepted the refused invocation")
	} else if failure, ok := errors.AsType[*DeliveryError](err); !ok || failure.Delivery != NotSent {
		t.Fatalf("attested conflict = %v, want not_sent", err)
	}
	if _, err := wrongClient.Invoke(ctx, invocation); err == nil {
		t.Fatal("accepted the unattested conflict")
	} else if failure, ok := errors.AsType[*DeliveryError](err); !ok || failure.Delivery != Unknown {
		t.Fatalf("unattested conflict = %v, want unknown", err)
	}

	callContext, cancelCall := context.WithCancel(ctx)
	defer cancelCall()
	invocation.Context.ContractRevision = "cancel"
	callDone := make(chan error, 1)
	go func() {
		_, err := client.Invoke(callContext, invocation)
		callDone <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("worker did not receive the invocation")
	}
	cancelCall()
	select {
	case <-canceled:
	case <-ctx.Done():
		t.Fatal("worker did not receive stream cancellation")
	}
	if err := <-callDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted invocation = %v, want context.Canceled", err)
	}
}
