package contractagent

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

func TestAgentSessionConcurrentRetentionPreservesSnapshotsAndEviction(t *testing.T) {
	view := func(marker string) *Manifest {
		return &Manifest{ContractRevision: "sha256:shared-contract", Resources: []Resource{{
			Address: "app/record/item", Kind: "scenery.record", Module: "app", Name: "item",
			Spec: map[string]any{"nested": map[string]any{"marker": marker}},
		}}}
	}
	result := &Result{
		Manifest: view("expanded"), WorkspaceRevision: "sha256:workspace-0",
		DeploymentRevisions: map[string]string{"production": "sha256:shared-deployment"},
		Diagnostics:         []Diagnostic{{Message: "retained diagnostic"}},
	}
	result.ViewManifests = map[string]*Manifest{"source": view("source"), "effective": view("effective"), "expanded": result.Manifest}
	session := NewAgentSession()
	request := AgentRequest{Method: "resources.get", Params: json.RawMessage(`{"address":"app/record/item"}`)}
	const workers = 16
	start := make(chan struct{})
	failures := make(chan *AgentError, workers)
	var group sync.WaitGroup
	for range workers {
		group.Go(func() {
			<-start
			for range 3 {
				if response := session.Handle(result, request); response.Error != nil {
					failures <- response.Error
					return
				}
			}
		})
	}
	close(start)
	group.Wait()
	close(failures)
	for failure := range failures {
		t.Fatal(failure)
	}
	if len(session.order) != 1 {
		t.Fatalf("concurrent first reads retained %d snapshots, want 1", len(session.order))
	}

	// Retention owns detached views even when the producer's references change.
	for _, manifest := range result.ViewManifests {
		manifest.Resources[0].Spec["nested"].(map[string]any)["marker"] = "mutated"
	}
	result.Diagnostics[0].Message = "mutated diagnostic"
	retained := session.snapshots[result.WorkspaceRevision]
	for name, manifest := range retained.Views {
		if got := manifest.Resources[0].Spec["nested"].(map[string]any)["marker"]; got != name {
			t.Fatalf("retained %s view marker = %v", name, got)
		}
	}
	if retained.Diagnostics[0].Message != "retained diagnostic" {
		t.Fatal("retained diagnostic aliases the producer")
	}
	copy, err := session.resolveSnapshot(nil, result.WorkspaceRevision)
	if err != nil {
		t.Fatal(err)
	}
	copy.Resources[0].Spec["nested"].(map[string]any)["marker"] = "changed returned copy"
	if retained.Manifest.Resources[0].Spec["nested"].(map[string]any)["marker"] != "expanded" {
		t.Fatal("returned snapshot aliases retained storage")
	}

	for index := 1; index <= 32; index++ {
		if response := session.Handle(result, request); response.Error != nil {
			t.Fatal(response.Error)
		}
		next := &Result{
			Manifest: view(fmt.Sprintf("generation-%d", index)), WorkspaceRevision: fmt.Sprintf("sha256:workspace-%d", index),
			DeploymentRevisions: result.DeploymentRevisions,
		}
		if response := session.Handle(next, request); response.Error != nil {
			t.Fatal(response.Error)
		}
	}
	if len(session.order) != 32 || session.snapshots[result.WorkspaceRevision] != nil {
		t.Fatal("repeated reads changed the 32-snapshot insertion-order eviction")
	}
	for _, alias := range []string{"sha256:shared-contract", "sha256:shared-deployment"} {
		manifest, err := session.resolveSnapshot(nil, alias)
		if err != nil {
			t.Fatal(err)
		}
		if got := manifest.Resources[0].Spec["nested"].(map[string]any)["marker"]; got != "generation-32" {
			t.Fatalf("shared alias %s resolved to %v", alias, got)
		}
	}
}
