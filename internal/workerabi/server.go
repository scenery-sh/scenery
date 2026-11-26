package workerabi

import (
	"context"
	"crypto/subtle"
	"encoding/binary"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
)

type Launch struct {
	WorkerSocket  string   `json:"worker_socket"`
	HostSocket    string   `json:"host_socket"`
	Token         string   `json:"token"`
	Identity      Identity `json:"identity"`
	ParentDeathFD int      `json:"parent_death_fd,omitempty"`
}

func ReadLaunch(args []string) (Launch, error) {
	if len(args) != 1 {
		return Launch{}, fmt.Errorf("worker requires one private launch file")
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		return Launch{}, err
	}
	var launch Launch
	if err := jsonv2.Unmarshal(data, &launch, jsonv2.RejectUnknownMembers(true)); err != nil {
		return Launch{}, fmt.Errorf("decode worker launch: %w", err)
	}
	if launch.Identity.ABI != Version || len(launch.Token) < 32 || launch.WorkerSocket == "" || launch.HostSocket == "" {
		return Launch{}, fmt.Errorf("invalid worker launch configuration")
	}
	return launch, nil
}

// Handler serves an initialized worker. It rejects mismatches before calling
// the operation adapter and keeps the request context alive through streaming.
func (launch Launch) Handler(invoke func(context.Context, Invocation) (Result, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		auth := request.Header.Values("Authorization")
		if len(auth) != 1 || subtle.ConstantTimeCompare([]byte(auth[0]), []byte("Bearer "+launch.Token)) != 1 {
			writeFault(w, http.StatusUnauthorized)
			return
		}
		w.Header().Set(ContractHeader, launch.Identity.ContractRevision)
		w.Header().Set(ImplementationHeader, launch.Identity.ImplementationRevision)
		switch {
		case request.Method == http.MethodGet && request.URL.Path == ReadyPath:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(launch.Identity)
		case request.Method == http.MethodPost && request.URL.Path == InvokePath:
			launch.serveInvocation(w, request, invoke)
		default:
			writeFault(w, http.StatusNotFound)
		}
	})
}

func (launch Launch) serveInvocation(w http.ResponseWriter, request *http.Request, invoke func(context.Context, Invocation) (Result, error)) {
	var invocation Invocation
	body, err := io.ReadAll(http.MaxBytesReader(w, request.Body, MaxMetadataBytes))
	if err != nil || jsonv2.Unmarshal(body, &invocation, jsonv2.RejectUnknownMembers(true)) != nil || len(invocation.Input) == 0 {
		writeFault(w, http.StatusBadRequest)
		return
	}
	if invocation.Service != launch.Identity.Service || invocation.Context.ContractRevision != launch.Identity.ContractRevision {
		writeFault(w, http.StatusConflict)
		return
	}
	if invocation.Mode != "direct" && invocation.Mode != "durable" || invocation.Context.InvocationID == "" || invocation.Context.Scope == "" {
		writeFault(w, http.StatusBadRequest)
		return
	}
	ctx := request.Context()
	if deadline := invocation.Context.Deadline; deadline != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, *deadline)
		defer cancel()
	}
	result, err := callOperation(ctx, invocation, invoke)
	if result.Stream != nil {
		closeStream := sync.OnceFunc(func() { _ = result.Stream.Close() })
		stop := context.AfterFunc(ctx, closeStream)
		defer closeStream()
		defer stop()
	}
	if err != nil || len(result.Outcome) == 0 || result.Stream != nil && result.StreamSize < 0 || result.Stream != nil && result.Events != nil {
		writeFault(w, http.StatusInternalServerError)
		return
	}
	metadata, err := jsonv2.Marshal(struct {
		Outcome json.RawMessage `json:"outcome"`
	}{Outcome: result.Outcome})
	if err != nil || len(metadata) > MaxMetadataBytes {
		writeFault(w, http.StatusInternalServerError)
		return
	}
	if result.Events != nil {
		w.Header().Set("Content-Type", EventsMediaType)
		header := binary.BigEndian.AppendUint32(nil, uint32(len(metadata)))
		if _, err := w.Write(header); err != nil {
			return
		}
		if _, err := w.Write(metadata); err != nil {
			return
		}
		controller := http.NewResponseController(w)
		if err := controller.Flush(); err != nil {
			return
		}
		if err := runEventProducer(ctx, result.Events, func(data []byte, id, event string) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			line, err := encodeEventLine(data, id, event)
			if err != nil {
				return err
			}
			if _, err := w.Write(line); err != nil {
				return err
			}
			return controller.Flush()
		}); err != nil {
			panic(http.ErrAbortHandler)
		}
		return
	}
	if result.Stream == nil {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(metadata)
		return
	}
	w.Header().Set("Content-Type", StreamMediaType)
	w.Header().Set(StreamSizeHeader, strconv.FormatInt(result.StreamSize, 10))
	header := binary.BigEndian.AppendUint32(nil, uint32(len(metadata)))
	if _, err := w.Write(header); err != nil {
		return
	}
	if _, err := w.Write(metadata); err != nil {
		return
	}
	if err := http.NewResponseController(w).Flush(); err != nil {
		return
	}
	if _, err := io.CopyN(w, result.Stream, result.StreamSize); err != nil {
		// Reset a partially written stream rather than finish it successfully.
		panic(http.ErrAbortHandler)
	}
}

func callOperation(ctx context.Context, invocation Invocation, invoke func(context.Context, Invocation) (Result, error)) (result Result, err error) {
	defer func() {
		if recover() != nil {
			err = ErrImplementation
		}
	}()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return invoke(ctx, invocation)
}

func writeFault(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, `{"error":"worker request failed"}`)
}

func runEventProducer(
	ctx context.Context,
	produce func(context.Context, func([]byte, string, string) error) error,
	emit func([]byte, string, string) error,
) (err error) {
	defer func() {
		if recover() != nil {
			err = ErrImplementation
		}
	}()
	return produce(ctx, emit)
}

func encodeEventLine(data []byte, id, event string) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty event payload")
	}
	if strings.ContainsAny(id, "\r\n\x00") || strings.ContainsAny(event, "\r\n\x00") {
		return nil, fmt.Errorf("event metadata contains forbidden control character")
	}
	frame := struct {
		Data  json.RawMessage `json:"data"`
		ID    string          `json:"id,omitempty"`
		Event string          `json:"event,omitempty"`
	}{
		Data:  json.RawMessage(data),
		ID:    id,
		Event: event,
	}
	line, err := json.Marshal(frame)
	if err != nil {
		return nil, err
	}
	return append(line, '\n'), nil
}
