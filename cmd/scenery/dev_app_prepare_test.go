package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	localagent "scenery.sh/internal/agent"
)

func TestAppStartInputsJoinAndRetireFailedPreparation(t *testing.T) {
	for _, fail := range []bool{false, true} {
		copyMayFinish := make(chan struct{})
		copyFinished := make(chan struct{})
		want := &devRuntimeEnvironment{base: []string{"EXAMPLE=value"}}
		failure := errors.New("environment failed")
		discarded := ""
		binary, environment, err := prepareAppStartInputs(func() (string, error) {
			<-copyMayFinish
			close(copyFinished)
			return "retained", nil
		}, func() (*devRuntimeEnvironment, error) {
			close(copyMayFinish)
			if fail {
				return nil, failure
			}
			return want, nil
		}, func(path string) { discarded = path })
		select {
		case <-copyFinished:
		default:
			t.Fatal("preparation returned before retained copy finished")
		}
		if fail {
			if !errors.Is(err, failure) || discarded != "retained" || binary != "" || environment != nil {
				t.Fatal("failed environment did not retire its joined copy")
			}
		} else if err != nil || discarded != "" || binary != "retained" || environment != want {
			t.Fatal("successful preparation lost its exact bytes or environment")
		}
	}
}

func TestAppStartInputsPreserveBothErrors(t *testing.T) {
	copyErr, environmentErr := errors.New("copy failed"), errors.New("environment failed")
	_, _, err := prepareAppStartInputs(func() (string, error) { return "", copyErr },
		func() (*devRuntimeEnvironment, error) { return nil, environmentErr },
		func(string) { t.Fatal("failed copy published a path") })
	if !errors.Is(err, copyErr) || !errors.Is(err, environmentErr) {
		t.Fatalf("preparation lost errors: %v", err)
	}
}

func TestAppStartInputFailurePreservesServingBinary(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "run", "app")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(dir, "current")
	s := &devSupervisor{agentSession: &localagent.Session{StateRoot: root}, current: &runningApp{
		launch: &appStartPlan{request: devProcessStartRequest{Command: current}},
	}}
	for _, path := range []string{current, filepath.Join(dir, "unused")} {
		if err := os.WriteFile(path, []byte("binary"), 0o700); err != nil {
			t.Fatal(err)
		}
		_, _, err := prepareAppStartInputs(func() (string, error) { return path, nil },
			func() (*devRuntimeEnvironment, error) { return nil, errors.New("failed environment") },
			func(path string) {
				s.releaseUnusedAppBinary(&appStartPlan{request: devProcessStartRequest{Command: path}})
			})
		if err == nil {
			t.Fatal("failed environment accepted")
		}
		_, statErr := os.Stat(path)
		if path == current && statErr != nil || path != current && !os.IsNotExist(statErr) {
			t.Fatalf("retention cleanup violated serving ownership: %v", statErr)
		}
	}
}
