package main

import (
	"fmt"
	"io"
	"os"
	"syscall"
	"time"

	"scenery.sh/internal/devtelemetry"
)

func writeDevTelemetryEvents(w io.Writer, events []devtelemetry.Event) error {
	for _, event := range events {
		app := ""
		if event.App != nil {
			app = event.App.Name + " "
		}
		var detail string
		if rebuild := event.Rebuild; rebuild != nil {
			detail = fmt.Sprintf("ok=%t %s detect %.1fms compile %.1fms link %.1fms ready %.1fms rebuilt %v skipped %v",
				rebuild.OK, rebuild.TriggerKind, rebuild.DetectMS, rebuild.CompileMS, rebuild.LinkMS, rebuild.ReadyMS, rebuild.RebuiltModules, rebuild.SkippedModules)
			if rebuild.EditToReadyMS != nil {
				detail += fmt.Sprintf(" edit to ready %.1fms", *rebuild.EditToReadyMS)
			}
			if rebuild.ErrorCode != "" {
				detail += " (" + rebuild.ErrorCode + ")"
			}
		} else if fault := event.WorkerFault; fault != nil {
			detail = fmt.Sprintf("%s (%s) exit %d %s", fault.Module, fault.Runtime, fault.ExitCode, fault.Signal)
		}
		if _, err := fmt.Fprintf(w, "%s %s%s generation %d: %s\n", event.At.Local().Format(time.DateTime), app, event.Kind, event.Generation, detail); err != nil {
			return err
		}
	}
	return nil
}

func recordDevRebuild(app *devtelemetry.App, timing workerRebuildTiming, trigger, errorCode string) {
	if timing.Generation == 0 {
		return
	}
	rebuild := &devtelemetry.Rebuild{
		DetectMS: timing.DetectMS, CompileMS: timing.CompileMS, LinkMS: timing.LinkMS, ReadyMS: timing.ReadyMS,
		RebuiltModules: timing.RebuiltModules, SkippedModules: timing.SkippedModules, TriggerKind: trigger,
		OK: errorCode == "", ErrorCode: errorCode,
	}
	if rebuild.OK {
		rebuild.EditToReadyMS = &timing.TotalMS
	}
	appendTelemetryRecord(devtelemetry.Event{At: time.Now().UTC(), Kind: "rebuild", App: app, Generation: timing.Generation, Rebuild: rebuild})
}

func recordDevWorkerFault(app *devtelemetry.App, generation uint64, module, runtime string, status *os.ProcessState) {
	if status == nil {
		return
	}
	var signal int
	if value, ok := status.Sys().(syscall.WaitStatus); ok && value.Signaled() {
		signal = int(value.Signal())
	}
	recordDevWorkerExit(app, generation, module, runtime, status.ExitCode(), signal)
}

func recordDevWorkerExit(app *devtelemetry.App, generation uint64, module, runtime string, exitCode, signal int) {
	if app == nil || generation == 0 || module == "" {
		return
	}
	fault := &devtelemetry.WorkerFault{Module: module, Runtime: runtime, ExitCode: exitCode}
	if signal != 0 {
		fault.Signal = syscall.Signal(signal).String()
	}
	appendTelemetryRecord(devtelemetry.Event{At: time.Now().UTC(), Kind: "worker_fault", App: app, Generation: generation, WorkerFault: fault})
}
