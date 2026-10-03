package graph

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"
)

// Compare traversal and pagination with all-pairs shortest paths, independently
// of the query's frontier algorithm. Seeds cover cycles, repeated references,
// isolated nodes and overlapping focus closures.
func FuzzGraphQueryReachability(f *testing.F) {
	f.Add([]byte{0, 0, 0, 0, 0, 0})
	f.Add([]byte{1, 0, 3, 0, 0, 0})
	f.Add([]byte{7, 0, 3, 1, 6, 2, 0, 1, 1, 2, 2, 3, 3, 1, 5, 6})
	f.Add([]byte{7, 1, 1, 4, 5, 3, 0, 1, 1, 2, 2, 3, 3, 4, 3, 4})
	f.Add([]byte{7, 2, 2, 0, 7, 1, 0, 1, 1, 2, 2, 3, 3, 4, 7, 6})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 6 {
			return
		}
		data = data[:min(len(data), 518)]
		size := 1 + int(data[0]%16)
		direction := []string{"dependencies", "dependents", "both"}[data[1]%3]
		depth := int(data[2] % 5)
		focus := int(data[3]) % size
		otherFocus := int(data[4]) % size
		limit := 1 + int(data[5])%size
		manifest := &Manifest{ContractRevision: "sha256:query-test", Resources: make([]Resource, size)}
		distance := make([][]int, size)
		for i := range size {
			manifest.Resources[i] = Resource{Address: fmt.Sprintf("test/record/r%02d", i), Module: "test", Kind: "scenery.record", Spec: map[string]any{"references": []any{}}}
			distance[i] = make([]int, size)
			for j := range size {
				if i != j {
					distance[i][j] = AgentMaxDepth + 1
				}
			}
		}
		var edges []GraphEdge
		for i := 6; i+1 < len(data); i += 2 {
			from, to := int(data[i])%size, int(data[i+1])%size
			refs := manifest.Resources[from].Spec["references"].([]any)
			edges = append(edges, GraphEdge{From: manifest.Resources[from].Address, To: manifest.Resources[to].Address, Path: fmt.Sprintf("/spec/references/%d", len(refs))})
			manifest.Resources[from].Spec["references"] = append(refs, map[string]any{"$ref": fmt.Sprintf("record.r%02d", to)})
			if direction != "dependents" {
				distance[from][to] = min(distance[from][to], 1)
			}
			if direction != "dependencies" {
				distance[to][from] = min(distance[to][from], 1)
			}
		}
		for k := range size {
			for i := range size {
				for j := range size {
					distance[i][j] = min(distance[i][j], distance[i][k]+distance[k][j])
				}
			}
		}
		var graphAddresses, contextAddresses []string
		for i, resource := range manifest.Resources {
			if distance[focus][i] <= max(depth, 1) {
				graphAddresses = append(graphAddresses, resource.Address)
			}
			if min(distance[focus][i], distance[otherFocus][i]) <= max(depth, 1) {
				contextAddresses = append(contextAddresses, resource.Address)
			}
		}
		before, _ := json.Marshal(manifest)
		result, err := Graph(manifest, manifest.Resources[focus].Address, GraphOptions{Direction: direction, Depth: depth, MaxResources: limit})
		if err != nil {
			t.Fatal(err)
		}
		if result.Truncated != (len(graphAddresses) > limit) {
			t.Fatalf("truncated = %t, reachable = %d, limit = %d", result.Truncated, len(graphAddresses), limit)
		}
		graphAddresses = graphAddresses[:min(len(graphAddresses), limit)]
		checkResourceAddresses(t, result.Resources, graphAddresses)
		wantEdges := make([]GraphEdge, 0)
		for _, edge := range edges {
			if slices.Contains(graphAddresses, edge.From) && slices.Contains(graphAddresses, edge.To) {
				wantEdges = append(wantEdges, edge)
			}
		}
		slices.SortFunc(wantEdges, func(a, b GraphEdge) int {
			return slices.Compare([]string{a.From, a.To, a.Path}, []string{b.From, b.To, b.Path})
		})
		if !reflect.DeepEqual(result.Edges, wantEdges) {
			t.Fatalf("edges = %#v, want %#v", result.Edges, wantEdges)
		}
		options := ContextOptions{Focus: []string{manifest.Resources[focus].Address, manifest.Resources[otherFocus].Address}, Include: []string{direction}, Depth: depth, MaxResources: limit, MaxBytes: AgentMaxBytes}
		if direction == "both" {
			options.Include = []string{"dependencies", "dependents"}
		}
		var resources []Resource
		for page := 0; ; page++ {
			if page >= size {
				t.Fatal("context pagination did not finish")
			}
			bundle, err := ContextAt(manifest, "workspace", nil, options, time.Unix(1_800_000_000, 0))
			if err != nil {
				t.Fatal(err)
			}
			resources = append(resources, bundle.Resources...)
			if !bundle.Truncated {
				break
			}
			options.ContinuationToken = bundle.ContinuationToken
		}
		checkResourceAddresses(t, resources, contextAddresses)
		after, _ := json.Marshal(manifest)
		if string(before) != string(after) {
			t.Fatal("query changed its source manifest")
		}
	})
}

func checkResourceAddresses(t *testing.T, resources []Resource, want []string) {
	t.Helper()
	got := make([]string, 0, len(resources))
	for _, resource := range resources {
		got = append(got, resource.Address)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("resource addresses = %v, want %v", got, want)
	}
}
