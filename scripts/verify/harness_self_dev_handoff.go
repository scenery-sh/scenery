package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
)

// The lifetime-held OS lock is a real write-generation exclusion assertion.
// It is acquired by the service constructor, never by package initialization.
func prepareHarnessHandoffService(appRoot string) error {
	source := `package service
import (
  "context"
  "fmt"
  "os"
  "path/filepath"
  "syscall"
  servicecontract "example.com/basicapp/service/scenerycontract"
)
type Service struct { writerLock *os.File }
func NewService(context.Context, servicecontract.ServiceConstructorInput) (*Service, error) {
  lock, err := os.OpenFile(filepath.Join(".scenery", "probe-writer.lock"), os.O_CREATE|os.O_RDWR, 0600)
  if err != nil { return nil, err }
  if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
    _ = lock.Close()
    return nil, fmt.Errorf("overlapping write generations: %w", err)
  }
  if false { return nil, fmt.Errorf("intentional candidate startup failure") }
  return &Service{writerLock: lock}, nil
}
func (*Service) Echo(_ context.Context, input servicecontract.EchoInput) (servicecontract.EchoOutcome, error) {
  return servicecontract.EchoOk{Value: servicecontract.EchoResult{Message: "echo:" + input.Message}}, nil
}
`
	return os.WriteFile(filepath.Join(appRoot, "service/api.go"), []byte(source), 0o600)
}

func runHarnessAppHandoffProbe(parent context.Context, root, home string, started detachedDevResult) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()
	paths, err := localagent.PathsForWorktree(home, root)
	if err != nil {
		return nil, err
	}
	client := localagent.NewClient(paths.Socket)
	defer client.CloseIdleConnections()
	readSession := func() (localagent.Session, error) {
		sessions, err := client.List(ctx, paths.AppRoot)
		if err != nil {
			return localagent.Session{}, err
		}
		if len(sessions) != 1 || sessions[0].OwnerPID != started.PID || sessions[0].AppRoot != paths.AppRoot {
			return localagent.Session{}, fmt.Errorf("handoff lost exact-root supervisor ownership")
		}
		return sessions[0], nil
	}
	apiURL := strings.TrimRight(started.Session.RouteManifest.Routes[localagent.RouteAPI].URL, "/") + "/echo"
	if err := harnessHandoffEcho(ctx, apiURL, "echo:handoff"); err != nil {
		return nil, err
	}
	initial, err := readSession()
	if err != nil {
		return nil, err
	}
	sourcePath := filepath.Join(root, "service/api.go")
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		return nil, err
	}
	// Invalid test syntax makes any accidental ordinary rebuild observable.
	if err := os.WriteFile(filepath.Join(root, "service/ignored_test.go"), []byte("package service\nfunc invalid test syntax\n"), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("Documentation-only runtime edit probe.\n"), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(sourcePath, source, 0o600); err != nil {
		return nil, err
	}
	if err := harnessWaitContext(ctx, time.Second); err != nil {
		return nil, err
	}
	unchanged, err := readSession()
	if err != nil || unchanged.AppPID != initial.AppPID || unchanged.Status != "running" {
		return nil, fmt.Errorf("test/doc/identical-content edits changed the runtime: %+v: %v", unchanged, err)
	}
	if err := os.Remove(filepath.Join(root, "service/ignored_test.go")); err != nil {
		return nil, err
	}
	rejectedPath := filepath.Join(root, "service/preflight_reject.go")
	rejected := "package service\nimport \"os\"\nfunc init() { if len(os.Args) == 2 && os.Args[1] == \"--scenery-runtime-preflight\" { os.Exit(19) } }\n"
	if err := os.WriteFile(rejectedPath, []byte(rejected), 0o600); err != nil {
		return nil, err
	}
	for {
		log, err := os.ReadFile(started.LogPath)
		if err != nil {
			return nil, err
		}
		if bytes.Contains(log, []byte("candidate runtime preflight failed")) {
			break
		}
		if err := harnessWaitContext(ctx, 100*time.Millisecond); err != nil {
			return nil, fmt.Errorf("candidate did not reach preflight rejection: %w", err)
		}
	}
	preserved, err := readSession()
	if err != nil || preserved.AppPID != initial.AppPID || preserved.Status != "running" {
		return nil, fmt.Errorf("rejected candidate displaced the live generation: %+v: %v", preserved, err)
	}
	if err := harnessHandoffEcho(ctx, apiURL, "echo:handoff"); err != nil {
		return nil, err
	}
	if err := os.Remove(rejectedPath); err != nil {
		return nil, err
	}
	waitReplacement := func(previousPID, response string) (localagent.Session, error) {
		var last error
		for {
			session, readErr := readSession()
			if readErr == nil && session.Status == "running" && session.AppPID != "" && session.AppPID != previousPID {
				if last = harnessHandoffEcho(ctx, apiURL, response); last == nil {
					return session, nil
				}
			} else {
				last = readErr
			}
			if err := harnessWaitContext(ctx, 100*time.Millisecond); err != nil {
				return localagent.Session{}, fmt.Errorf("wait for serving replacement: %w: %v", err, last)
			}
		}
	}
	failed := bytes.Replace(source, []byte("if false {"), []byte("if true {"), 1)
	if err := os.WriteFile(sourcePath, failed, 0o600); err != nil {
		return nil, err
	}
	recovered, err := waitReplacement(initial.AppPID, "echo:handoff")
	if err != nil {
		return nil, fmt.Errorf("failed candidate recovery: %w", err)
	}
	changed := bytes.Replace(source, []byte(`Message: "echo:"`), []byte(`Message: "changed:"`), 1)
	if err := os.WriteFile(sourcePath, changed, 0o600); err != nil {
		return nil, err
	}
	updated, err := waitReplacement(recovered.AppPID, "changed:handoff")
	if err != nil {
		return nil, fmt.Errorf("correct served revision after recovery: %w", err)
	}
	return map[string]any{
		"test_doc_identical_edits_no_restart":   true,
		"failed_start_restored_previous":        true,
		"preflight_rejection_preserved_backend": true,
		"exclusive_writer_lock":                 true,
		"runtime_edit_served_correct_response":  true,
		"initial_pid":                           initial.AppPID, "restored_pid": recovered.AppPID, "updated_pid": updated.AppPID,
	}, nil
}

func harnessHandoffEcho(ctx context.Context, url, want string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(`{"message":"handoff"}`))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	if err != nil {
		return err
	}
	var result struct {
		Message string `json:"message"`
	}
	if response.StatusCode != http.StatusOK || json.Unmarshal(data, &result) != nil || result.Message != want {
		return fmt.Errorf("served response %d %s; want %q", response.StatusCode, data, want)
	}
	return nil
}

func harnessWaitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
