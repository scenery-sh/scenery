package main

import "testing"

func TestHarnessPrivateExternalBuildEvidence(t *testing.T) {
	valid := make([]harnessWatchEvent, 0, 4)
	for _, name := range []string{"go.command", "build.shared_link_queue", "build.artifact", "build.request"} {
		event := harnessWatchEvent{Type: "build.step"}
		event.Data.Name, event.Data.OperationID, event.Data.OK = name, "current", true
		switch name {
		case "go.command":
			event.Data.Reason = "build"
		case "build.artifact":
			event.Data.Cache, event.Data.Reason, event.Data.ExecutableBytes = "miss", "linked_development_process_service_service", 1
		}
		valid = append(valid, event)
	}
	if evidence, err := harnessPrivateExternalBuildEvidence(valid); err != nil || evidence["build_backend"] != "stock_process_link" {
		t.Fatalf("evidence = %v, %v", evidence, err)
	}
	for _, name := range []string{"published", "unlinked", "uncompiled", "unbounded", "shared_action", "old_generation"} {
		t.Run(name, func(t *testing.T) {
			events := append([]harnessWatchEvent(nil), valid...)
			switch name {
			case "published", "shared_action":
				event := events[2]
				event.Data.Name = map[string]string{"published": "build.shared_artifact", "shared_action": "build.shared_queue"}[name]
				events = append(events, event)
			case "unlinked":
				events[2].Data.Reason = "linked_application_executable"
			case "uncompiled":
				events[0].Data.OK = false
			case "unbounded":
				events[1].Data.OK = false
			case "old_generation":
				events[3].Data.OperationID = "new-request"
			}
			if _, err := harnessPrivateExternalBuildEvidence(events); err == nil {
				t.Fatal("invalid external-source build proof was accepted")
			}
		})
	}
}
