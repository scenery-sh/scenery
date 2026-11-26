package main

import (
	"fmt"
	"io"
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
