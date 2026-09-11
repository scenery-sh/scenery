package worker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"scenery.sh/internal/nativeprotocol"
	"scenery.sh/internal/runtimeapp"
	"sort"
	"syscall"
	"time"
)

// Set only by the experiment's native build. The proof never echoes an expected
// input identity supplied by the process owner.
var linkedInputDigest string
var linkedImplementationRevision string
var linkedGoTarget string

type Proof struct {
	ProtocolRevision       string   `json:"protocol_revision"`
	Protocol               string   `json:"protocol"`
	ContractRevision       string   `json:"contract_revision"`
	InputDigest            string   `json:"input_digest"`
	ImplementationRevision string   `json:"implementation_revision"`
	GoTarget               string   `json:"go_target"`
	Target                 string   `json:"target"`
	PID                    int      `json:"pid"`
	Operations             []string `json:"operations"`
}

// Main uses an inherited listener (fd 3) and a private stdin control channel.
// The owner reads the compiled proof before sending the activation message.
// EOF from that owner drains the worker. No environment switch selects this path.
func Main(appName, contract string, admissions []Admission, bindings []SQLBinding, register func() error) error {
	if appName == "" || contract == "" || linkedInputDigest == "" || linkedImplementationRevision == "" || linkedGoTarget == "" {
		return fmt.Errorf("native worker has no compiled identity")
	}
	control := bufio.NewReader(os.Stdin)
	var setup struct {
		Token  string `json:"token"`
		Listen string `json:"listen"`
	}
	if err := readControl(control, &setup); err != nil {
		return err
	}
	if _, _, err := net.SplitHostPort(setup.Listen); err != nil {
		return fmt.Errorf("native worker requires the public kernel listener address")
	}
	if len(setup.Token) < 32 {
		return fmt.Errorf("native worker requires a private channel token")
	}
	// Registration retains native callbacks but does not execute constructors.
	if err := register(); err != nil {
		return err
	}
	application.mu.RLock()
	operations := make([]string, 0, len(application.operations))
	for address := range application.operations {
		operations = append(operations, address)
	}
	application.mu.RUnlock()
	sort.Strings(operations)
	if err := json.NewEncoder(os.Stdout).Encode(Proof{Protocol: Protocol, ProtocolRevision: nativeprotocol.Revision, ContractRevision: contract, InputDigest: linkedInputDigest, ImplementationRevision: linkedImplementationRevision, GoTarget: linkedGoTarget, Target: runtime.GOOS + "/" + runtime.GOARCH, PID: os.Getpid(), Operations: operations}); err != nil {
		return err
	}
	var activation struct {
		Activate bool `json:"activate"`
	}
	if err := readControl(control, &activation); err != nil {
		return err
	}
	if !activation.Activate {
		return fmt.Errorf("native worker activation was not granted")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() { _, _ = io.Copy(io.Discard, control); cancel() }()
	handler, err := NewHandler(application, setup.Token, contract, linkedInputDigest, admissions)
	if err != nil {
		return err
	}
	file := os.NewFile(3, "scenery-native-worker-listener")
	if file == nil {
		return fmt.Errorf("native worker listener is missing")
	}
	listener, err := net.FileListener(file)
	_ = file.Close()
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	application.mu.Lock()
	application.metadata = runtimeapp.ResolveMetadata(appName, setup.Listen)
	application.mu.Unlock()
	if err := ConfigureSQLBindings(bindings); err != nil {
		return err
	}
	if err := application.services.Initialize(ctx); err != nil {
		shutdownCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		return errors.Join(err, application.services.Shutdown(shutdownCtx))
	}
	if err := ctx.Err(); err != nil {
		shutdownCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		return errors.Join(err, application.services.Shutdown(shutdownCtx))
	}
	server := &http.Server{BaseContext: func(net.Listener) context.Context { return ctx }, Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	if err := json.NewEncoder(os.Stdout).Encode(struct {
		Ready bool `json:"ready"`
		PID   int  `json:"pid"`
	}{true, os.Getpid()}); err != nil {
		_ = Drain(server, application)
		return err
	}
	select {
	case <-ctx.Done():
		return Drain(server, application)
	case err := <-served:
		return errors.Join(err, Drain(server, application))
	}
}

func readControl(reader *bufio.Reader, value any) error {
	line, err := reader.ReadSlice('\n')
	if err != nil {
		return fmt.Errorf("native worker control: %w", err)
	}
	return decodeOne(bytes.NewReader(line), value)
}
