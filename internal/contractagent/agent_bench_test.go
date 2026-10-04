package contractagent

import (
	"encoding/json"
	"fmt"
	"testing"
)

func BenchmarkAgentRead(b *testing.B) {
	for _, size := range []int{8, 128, 1024} {
		result := benchmarkAgentResult(size)
		context, _ := json.Marshal(ContextOptions{
			Focus: []string{result.Manifest.Resources[0].Address}, Include: []string{"dependencies", "schemas"},
			Depth: 3, MaxResources: 1000, MaxBytes: 2_000_000,
		})
		for _, request := range []AgentRequest{
			{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "resources.get", Params: json.RawMessage(`{"address":"bench/record/item0000"}`)},
			{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "context.get", Params: context},
		} {
			b.Run(fmt.Sprintf("resources=%d/%s", size, request.Method), func(b *testing.B) {
				session := NewAgentSession()
				if response := session.Handle(result, request); response.Error != nil {
					b.Fatal(response.Error)
				}
				b.ReportAllocs()
				for b.Loop() {
					response := session.Handle(result, request)
					if response.Error != nil {
						b.Fatal(response.Error)
					}
					if _, err := json.Marshal(response); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func BenchmarkAgentFirstRead(b *testing.B) {
	for _, test := range []struct {
		size  int
		views bool
	}{{8, false}, {8, true}, {128, true}, {1024, true}} {
		b.Run(fmt.Sprintf("resources=%d/views=%t", test.size, test.views), func(b *testing.B) {
			result := benchmarkAgentResult(test.size)
			if !test.views {
				result.ViewManifests = nil
			}
			request := AgentRequest{Method: "resources.get", Params: json.RawMessage(`{"address":"bench/record/item0000"}`)}
			b.ReportAllocs()
			for b.Loop() {
				response := NewAgentSession().Handle(result, request)
				if response.Error != nil {
					b.Fatal(response.Error)
				}
				if _, err := json.Marshal(response); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkSelectedResources(b *testing.B) {
	for _, size := range []int{8, 1024} {
		manifest := benchmarkAgentResult(size).Manifest
		all := make([]string, size)
		for index, resource := range manifest.Resources {
			all[index] = resource.Address
		}
		for _, test := range []struct {
			name      string
			addresses []string
			missing   bool
		}{
			{name: "empty"},
			{name: "first", addresses: all[:1]},
			{name: "last", addresses: all[len(all)-1:]},
			{name: "two", addresses: []string{all[0], all[len(all)-1]}},
			{name: "all", addresses: all},
			{name: "missing", addresses: []string{"bench/record/missing"}, missing: true},
		} {
			b.Run(fmt.Sprintf("resources=%d/%s", size, test.name), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					resources, err := selectedResources(manifest, test.addresses)
					if (err != nil) != test.missing || (!test.missing && len(resources) != len(test.addresses)) {
						b.Fatalf("selection = %d resources, %v", len(resources), err)
					}
				}
			})
		}
	}
}

func benchmarkAgentResult(size int) *Result {
	manifest := &Manifest{ContractRevision: "sha256:contract", Resources: make([]Resource, size)}
	for index := range manifest.Resources {
		name := fmt.Sprintf("item%04d", index)
		manifest.Resources[index] = Resource{
			Address: "bench/record/" + name, Kind: "scenery.record", Name: name, Module: "bench",
			Spec: map[string]any{
				"description": "representative resource",
				"references":  []any{map[string]any{"$ref": fmt.Sprintf("record.item%04d", (index+1)%size)}},
			},
		}
	}
	return &Result{
		Manifest: manifest, WorkspaceRevision: "sha256:workspace",
		ViewManifests: map[string]*Manifest{"source": manifest, "effective": manifest, "expanded": manifest},
	}
}
