package main

import "testing"

func TestHarnessPrivateExternalBuildEvidence(t *testing.T) {
	valid := make([]harnessWatchEvent, 0, 5)
	for _, name := range []string{"go.command", "build.shared_input_check", "build.shared_link_queue", "build.shared_artifact", "build.request"} {
		event := harnessWatchEvent{Type: "build.step"}
		event.Data.Name, event.Data.OperationID, event.Data.OK = name, "current", true
		if name == "go.command" {
			event.Data.Reason = "build"
		}
		if name == "build.shared_artifact" {
			event.Data.Cache, event.Data.Reason, event.Data.ExecutableBytes = "bypass", "inputs_outside_shared_reuse_domain", 1
		}
		valid = append(valid, event)
	}
	if _, err := harnessPrivateExternalBuildEvidence(valid); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cached", "published", "unchecked", "uncompiled", "unbounded", "shared_action", "old_generation"} {
		t.Run(name, func(t *testing.T) {
			events := append([]harnessWatchEvent(nil), valid...)
			switch name {
			case "cached":
				events[3].Data.Cache = "hit"
			case "published":
				events[3].Data.Cache, events[3].Data.Reason = "miss", "linked_and_published"
			case "unchecked":
				events[1].Data.OK = false
			case "uncompiled":
				events[0].Data.OK = false
			case "unbounded":
				events[2].Data.OK = false
			case "shared_action":
				event := events[2]
				event.Data.Name = "build.shared_queue"
				events = append(events, event)
			case "old_generation":
				events[4].Data.OperationID = "new-request"
			}
			if _, err := harnessPrivateExternalBuildEvidence(events); err == nil {
				t.Fatal("invalid external-source build proof was accepted")
			}
		})
	}
}

func TestHarnessPrivateExternalBuildEvidenceAcceptsRetainedDefault(t *testing.T) {
	for _, test := range []struct {
		name, commandReason, commandCache, artifactCache, backendReason, backendCache string
	}{
		{name: "bootstrap", commandReason: "retained_bootstrap", commandCache: "miss", artifactCache: "retained_bootstrap", backendReason: "missing_recipe", backendCache: "miss"},
		{name: "compatible edit", commandReason: "build", commandCache: "retained_recipe", artifactCache: "retained_recipe", backendReason: "retained_compiler", backendCache: "hit"},
	} {
		t.Run(test.name, func(t *testing.T) {
			events := make([]harnessWatchEvent, 0, 4)
			for _, name := range []string{"go.command", "build.shared_link_queue", "build.artifact", "build.backend", "build.request"} {
				event := harnessWatchEvent{Type: "build.step"}
				event.Data.Name, event.Data.OperationID, event.Data.OK = name, "current", true
				switch name {
				case "go.command":
					event.Data.Reason, event.Data.Cache = test.commandReason, test.commandCache
				case "build.artifact":
					event.Data.Cache, event.Data.ExecutableBytes = test.artifactCache, 1
				case "build.backend":
					event.Data.Reason, event.Data.Cache = test.backendReason, test.backendCache
				}
				events = append(events, event)
			}
			if _, err := harnessPrivateExternalBuildEvidence(events); err != nil {
				t.Fatal(err)
			}
		})
	}
}
