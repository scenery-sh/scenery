package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func testNode(id ID, dependencies ...ID) Node {
	return Node{ID: id, Kind: "test", Dependencies: dependencies, Cache: CurrentValidation, Action: func(context.Context) error { return nil }}
}

func TestInvalidPlansRunNoActions(t *testing.T) {
	t.Parallel()
	tests := map[string][]Node{
		"duplicate":            {testNode("a"), testNode("a")},
		"cycle":                {testNode("a", "b"), testNode("b", "a")},
		"missing_dependency":   {testNode("a", "missing")},
		"missing_producer":     {{ID: "a", Kind: "test", Cache: Reusable, Inputs: []Reference{{Name: "result", Kind: "test", Producer: "missing"}}}},
		"missing_output":       {testNode("a"), {ID: "b", Kind: "test", Cache: Reusable, Dependencies: []ID{"a"}, Inputs: []Reference{{Name: "result", Kind: "test", Producer: "a"}}}},
		"overlapping_owners":   {{ID: "a", Kind: "test", Cache: Reusable, Outputs: []Reference{{Name: "a", Kind: "file", Path: "generated", Ownership: Managed}}}, {ID: "b", Kind: "test", Cache: Reusable, Outputs: []Reference{{Name: "b", Kind: "file", Path: "generated/child", Ownership: Managed}}}},
		"reusable_mutation":    {{ID: "a", Kind: "test", Cache: Reusable, Effects: []Capability{MutateDatabase}}},
		"reusable_publication": {{ID: "a", Kind: "test", Cache: Reusable, Effects: []Capability{PublishArtifacts}}},
	}
	for name, nodes := range tests {
		t.Run(name, func(t *testing.T) {
			calls := 0
			for i := range nodes {
				nodes[i].Action = func(context.Context) error { calls++; return nil }
			}
			if _, err := New(nodes); err == nil {
				t.Fatal("invalid graph admitted")
			}
			if calls != 0 {
				t.Fatal("validation ran actions")
			}
		})
	}
}

func TestSelectedClosureAndCapabilities(t *testing.T) {
	t.Parallel()
	var ran []ID
	nodes := []Node{testNode("unused"), testNode("root"), testNode("b", "root"), testNode("a", "root"), testNode("commit", "b", "a")}
	for i := range nodes {
		id := nodes[i].ID
		nodes[i].Action = func(context.Context) error { ran = append(ran, id); return nil }
	}
	nodes[4].Cache = ExternalEffect
	nodes[4].Effects = []Capability{PublishArtifacts}
	plan, err := New(nodes)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Run(context.Background(), []ID{"commit"}, nil); err == nil || len(ran) != 0 {
		t.Fatalf("forbidden effect ran: %v %v", ran, err)
	}
	if err := plan.Run(context.Background(), []ID{"commit"}, []Capability{PublishArtifacts}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ran, []ID{"root", "a", "b", "commit"}) {
		t.Fatalf("closure order = %v", ran)
	}
	metadata := plan.Nodes()
	metadata[0].Dependencies = append(metadata[0].Dependencies, "tampered")
	if err := plan.Run(context.Background(), []ID{"root"}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestFailuresAndCancellationBlockDependents(t *testing.T) {
	t.Parallel()
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "cancellation"}[cancel], func(t *testing.T) {
			ctx, stop := context.WithCancel(context.Background())
			defer stop()
			want := errors.New("failure")
			calls := 0
			a := testNode("a")
			a.Action = func(context.Context) error {
				if cancel {
					stop()
					return nil
				}
				return want
			}
			b := testNode("b", "a")
			b.Action = func(context.Context) error { calls++; return nil }
			plan, err := New([]Node{a, b})
			if err != nil {
				t.Fatal(err)
			}
			err = plan.Run(ctx, []ID{"b"}, nil)
			if calls != 0 || err == nil {
				t.Fatalf("dependent ran: %d %v", calls, err)
			}
			if cancel && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if !cancel && !errors.Is(err, want) {
				t.Fatal(err)
			}
		})
	}
}

func TestKeysBindExactInputsAndProducerIdentity(t *testing.T) {
	t.Parallel()
	node := testNode("render")
	node.Cache = Reusable
	node.Identity = map[string]string{"producer": "framework-a", "target": "go"}
	node.Inputs = []Reference{{Name: "z", Kind: "source", Digest: "one"}, {Name: "a", Kind: "source", Digest: "two"}}
	key, err := Key(node, map[string]string{"generated": "original"})
	if err != nil {
		t.Fatal(err)
	}
	reordered := node
	reordered.Inputs = []Reference{node.Inputs[1], node.Inputs[0]}
	same, err := Key(reordered, map[string]string{"generated": "original"})
	if err != nil || same != key {
		t.Fatalf("unstable canonical key: %s %s %v", key, same, err)
	}
	for _, edit := range []func(*Node){func(n *Node) { n.Identity = map[string]string{"producer": "framework-b", "target": "go"} }, func(n *Node) { n.Inputs = append([]Reference(nil), n.Inputs...); n.Inputs[0].Digest = "changed" }, func(n *Node) { n.Identity = map[string]string{"producer": "framework-a", "target": "typescript"} }} {
		changed := node
		edit(&changed)
		next, err := Key(changed, map[string]string{"generated": "original"})
		if err != nil || next == key {
			t.Fatalf("identity change reused key: %v", err)
		}
	}
	if changed, err := Key(node, map[string]string{"generated": "edited"}); err != nil || changed == key {
		t.Fatal("dependency output digest did not invalidate")
	}
}
