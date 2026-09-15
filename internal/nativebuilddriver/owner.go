package nativebuilddriver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type OwnerRequest struct {
	Protocol  string       `json:"protocol"`
	Session   string       `json:"session"`
	Workspace string       `json:"workspace"`
	Sequence  uint64       `json:"sequence"`
	Build     BuildRequest `json:"build"`
}

type OwnerResponse struct {
	Protocol string      `json:"protocol"`
	Result   BuildResult `json:"result"`
	Error    string      `json:"error,omitempty"`
}

// Owner keeps the captured recipe resident for one benchmark lane. Requests may
// overlap, but only the newest sequence is allowed to publish an executable.
type Owner struct {
	Recipe    *Recipe
	Session   string
	Workspace string
	StateRoot string

	mu       sync.Mutex
	latest   uint64
	cancel   context.CancelFunc
	listener net.Listener
}

func (owner *Owner) Serve(ctx context.Context, socket string) error {
	if owner.Recipe == nil || owner.Session == "" || owner.Workspace == "" {
		return fmt.Errorf("owner identity or recipe is incomplete")
	}
	if err := owner.Recipe.Validate(); err != nil {
		return fmt.Errorf("invalid retained recipe: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return err
	}
	if info, err := os.Lstat(socket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("refusing to replace non-socket %s", socket)
		}
		if err := os.Remove(socket); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	owner.listener = listener
	defer func() {
		_ = listener.Close()
		_ = os.Remove(socket)
	}()
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go owner.handle(ctx, conn)
	}
}

func (owner *Owner) handle(parent context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Minute))
	var request OwnerRequest
	response := OwnerResponse{Protocol: ProtocolVersion}
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&request); err != nil {
		response.Error = "invalid_request: " + err.Error()
		_ = json.NewEncoder(conn).Encode(response)
		return
	}
	result, err := owner.build(parent, request)
	response.Result = result
	if err != nil {
		response.Error = err.Error()
	}
	_ = json.NewEncoder(conn).Encode(response)
}

func (owner *Owner) build(parent context.Context, request OwnerRequest) (BuildResult, error) {
	if request.Protocol != ProtocolVersion || request.Session != owner.Session || filepath.Clean(request.Workspace) != filepath.Clean(owner.Workspace) || filepath.Clean(request.Build.Workspace) != filepath.Clean(owner.Workspace) {
		return BuildResult{Status: "unsupported", Reason: "foreign_owner_identity"}, nil
	}
	owner.mu.Lock()
	if request.Sequence <= owner.latest {
		owner.mu.Unlock()
		return BuildResult{Status: "unsupported", Reason: "stale_generation"}, nil
	}
	owner.latest = request.Sequence
	if owner.cancel != nil {
		owner.cancel()
	}
	ctx, cancel := context.WithCancel(parent)
	owner.cancel = cancel
	owner.mu.Unlock()

	destination := request.Build.Output
	candidate := filepath.Join(request.Build.GenerationRoot, "candidate")
	request.Build.Output = candidate
	result, err := owner.Recipe.Build(ctx, request.Build)
	result.Owner, result.RequestSequence = owner.Session, request.Sequence
	if err != nil {
		_ = os.Remove(candidate)
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			result.Status, result.Reason = "unsupported", "superseded_or_canceled"
			return result, nil
		}
		return result, err
	}
	if result.Status != "supported_and_rebuilt" {
		_ = os.Remove(candidate)
		return result, nil
	}
	commitStarted := time.Now()
	next, commitStats, err := owner.Recipe.Advance(result, owner.StateRoot)
	if err != nil {
		_ = os.Remove(candidate)
		return result, err
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.latest != request.Sequence {
		_ = os.Remove(candidate)
		result.Status, result.Reason = "unsupported", "stale_generation"
		return result, nil
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		_ = os.Remove(candidate)
		return result, err
	}
	if err := os.Rename(candidate, destination); err != nil {
		_ = os.Remove(candidate)
		return result, err
	}
	owner.Recipe = next
	result.Phases["state_commit"] = PhaseTiming{
		StartedAt: commitStarted.UTC(), DurationMS: elapsedMS(commitStarted),
		FilesHashed: commitStats.FilesHashed, BytesHashed: commitStats.BytesHashed,
		FilesReused: commitStats.FilesReused, BytesReused: commitStats.BytesReused,
	}
	if err := next.PruneUnreferenced(owner.StateRoot); err != nil {
		return result, fmt.Errorf("prune retained state: %w", err)
	}
	result.TransactionMS = elapsedMS(result.StartedAt)
	return result, nil
}

func RequestOwner(ctx context.Context, socket string, request OwnerRequest) (OwnerResponse, error) {
	var response OwnerResponse
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", socket)
	if err != nil {
		return response, err
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return response, err
		}
	}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return response, err
	}
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&response); err != nil {
		return response, err
	}
	if response.Protocol != ProtocolVersion {
		return response, fmt.Errorf("owner protocol mismatch %q", response.Protocol)
	}
	return response, nil
}
