package runtime

import (
	"context"
	"os"
	"strconv"
	"time"
)

var (
	supervisorParentCheckInterval = time.Second
	supervisorParentPID           = os.Getppid
	supervisorProcessExists       = processExists
)

func startSupervisorParentMonitor(cancel context.CancelFunc) func() {
	if !parentMonitorEnabled() {
		return func() {}
	}

	supervisorPID := parentMonitorPIDFromEnv()
	initial := supervisorParentPID()
	if supervisorPID <= 1 && initial <= 1 {
		return func() {}
	}

	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(supervisorParentCheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if supervisorParentMonitorShouldCancel(supervisorPID, supervisorProcessExists(supervisorPID), initial, supervisorParentPID()) {
					cancel()
					return
				}
			}
		}
	}()
	return func() {
		close(done)
	}
}

func parentMonitorEnabled() bool {
	return launchedBySupervisor() || os.Getenv("ONLAVA_PARENT_MONITOR") == "1"
}

func parentMonitorPIDFromEnv() int {
	value := os.Getenv("ONLAVA_PARENT_MONITOR_PID")
	if value == "" {
		value = os.Getenv("ONLAVA_DEV_SUPERVISOR_PID")
	}
	if value == "" {
		return 0
	}
	pid, err := strconv.Atoi(value)
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

func supervisorPIDFromEnv() int {
	return parentMonitorPIDFromEnv()
}

func supervisorParentMonitorShouldCancel(supervisorPID int, supervisorAlive bool, initial, current int) bool {
	if supervisorPID > 1 {
		return !supervisorAlive
	}
	return initial > 1 && current > 0 && current != initial
}
