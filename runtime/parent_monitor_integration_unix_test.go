//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestSupervisorParentMonitorCancelsWhenWrapperDies(t *testing.T) {
	if os.Getenv("ONLAVA_TEST_PARENT_MONITOR_HELPER") == "1" {
		runSupervisorParentMonitorHelper()
		return
	}

	dir := t.TempDir()
	readyPath := filepath.Join(dir, "ready")
	donePath := filepath.Join(dir, "done")

	wrapper := exec.Command("sleep", "30")
	if err := wrapper.Start(); err != nil {
		t.Fatalf("start fake wrapper: %v", err)
	}
	defer func() {
		if wrapper.Process != nil {
			_ = wrapper.Process.Kill()
			_, _ = wrapper.Process.Wait()
		}
	}()

	helper := exec.Command(os.Args[0], "-test.run=TestSupervisorParentMonitorCancelsWhenWrapperDies")
	helper.Env = append(os.Environ(),
		"ONLAVA_TEST_PARENT_MONITOR_HELPER=1",
		"ONLAVA_DEV_SUPERVISOR=1",
		"ONLAVA_DEV_SUPERVISOR_PID="+strconv.Itoa(wrapper.Process.Pid),
		"ONLAVA_TEST_READY="+readyPath,
		"ONLAVA_TEST_DONE="+donePath,
	)
	helper.Stdin = nil
	if err := helper.Start(); err != nil {
		t.Fatalf("start monitor helper: %v", err)
	}
	helperDone := make(chan error, 1)
	go func() { helperDone <- helper.Wait() }()
	defer func() {
		if helper.Process != nil {
			_ = helper.Process.Kill()
		}
		select {
		case <-helperDone:
		case <-time.After(time.Second):
		}
	}()

	waitForRuntimeTestFile(t, readyPath, time.Second)
	if err := wrapper.Process.Kill(); err != nil {
		t.Fatalf("kill fake wrapper: %v", err)
	}
	_, _ = wrapper.Process.Wait()
	wrapper.Process = nil

	waitForRuntimeTestFile(t, donePath, 2*time.Second)
	select {
	case err := <-helperDone:
		if err != nil {
			t.Fatalf("monitor helper exit = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("monitor helper did not exit after wrapper death")
	}
}

func runSupervisorParentMonitorHelper() {
	prevInterval := supervisorParentCheckInterval
	supervisorParentCheckInterval = 20 * time.Millisecond
	defer func() { supervisorParentCheckInterval = prevInterval }()

	ctx, cancel := context.WithCancel(context.Background())
	stop := startSupervisorParentMonitor(cancel)
	defer stop()

	_ = os.WriteFile(os.Getenv("ONLAVA_TEST_READY"), []byte("ready"), 0o644)
	<-ctx.Done()
	_ = os.WriteFile(os.Getenv("ONLAVA_TEST_DONE"), []byte("done"), 0o644)
}

func waitForRuntimeTestFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}
