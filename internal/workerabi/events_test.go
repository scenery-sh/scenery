package workerabi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEventStream(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "w-")
	if err != nil {
		t.Fatalf("mktemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "w.sock")
	identity := Identity{
		ABI:                    Version,
		Service:                "app/service/ticker",
		ContractRevision:       "sha256:contract",
		ImplementationRevision: "sha256:impl",
	}
	launch := Launch{
		WorkerSocket: socket,
		HostSocket:   socket,
		Token:        strings.Repeat("a", 32),
		Identity:     identity,
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	server := &http.Server{
		Protocols: Protocols(),
		Handler: launch.Handler(func(ctx context.Context, inv Invocation) (Result, error) {
			switch string(inv.Input) {
			case `"ok"`:
				return Result{
					Outcome: json.RawMessage(`{"kind":"result","name":"event","value":null}`),
					Events: func(ctx context.Context, emit func([]byte, string, string) error) error {
						if err := emit([]byte(`{"seq":"1"}`), "1", "tick"); err != nil {
							return err
						}
						return emit([]byte(`{"seq":"2"}`), "", "")
					},
				}, nil
			case `"err"`:
				return Result{
					Outcome: json.RawMessage(`{"kind":"error","name":"missing","problem":{"code":"missing","message":"gone"}}`),
				}, nil
			default:
				return Result{
					Outcome: json.RawMessage(`{"kind":"result","name":"event","value":null}`),
					Events: func(ctx context.Context, emit func([]byte, string, string) error) error {
						if err := emit([]byte(`{"seq":"1"}`), "1", "tick"); err != nil {
							return err
						}
						return errors.New("stream exploded")
					},
				}, nil
			}
		}),
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		_ = listener.Close()
	})

	client := NewClient(socket, launch.Token, identity)
	t.Cleanup(client.Close)

	baseInv := func(input string) Invocation {
		return Invocation{
			Service:   identity.Service,
			Operation: "app/operation/watch",
			Mode:      "direct",
			Context: InvocationContext{
				InvocationID:     "inv-1",
				Scope:            "scope-1",
				Generation:       1,
				ContractRevision: identity.ContractRevision,
			},
			Input: json.RawMessage(input),
		}
	}

	res, err := client.Invoke(context.Background(), baseInv(`"ok"`))
	if err != nil {
		t.Fatalf("invoke ok: %v", err)
	}
	defer func() { _ = res.Stream.Close() }()
	if res.Stream == nil || res.StreamSize != -1 {
		t.Fatalf("unexpected stream metadata: stream=%v size=%d", res.Stream != nil, res.StreamSize)
	}
	if string(res.Outcome) != `{"kind":"result","name":"event","value":null}` {
		t.Fatalf("unexpected outcome: %s", res.Outcome)
	}
	scanner := bufio.NewScanner(res.Stream)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan event stream: %v", err)
	}
	if len(lines) != 2 || lines[0] != `{"data":{"seq":"1"},"id":"1","event":"tick"}` || lines[1] != `{"data":{"seq":"2"}}` {
		t.Fatalf("unexpected event lines: %#v", lines)
	}

	errRes, err := client.Invoke(context.Background(), baseInv(`"err"`))
	if err != nil {
		t.Fatalf("invoke pre-stream error: %v", err)
	}
	if errRes.Stream != nil {
		t.Fatal("pre-stream error unexpectedly opened stream")
	}

	abortRes, err := client.Invoke(context.Background(), baseInv(`"abort"`))
	if err != nil {
		t.Fatalf("invoke mid-stream abort: %v", err)
	}
	defer func() { _ = abortRes.Stream.Close() }()
	if _, err := io.ReadAll(abortRes.Stream); err == nil {
		t.Fatal("aborted event stream ended with clean EOF")
	}
}
