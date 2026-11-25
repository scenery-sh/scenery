package main

import (
	"slices"
	"testing"

	"scenery.sh/internal/graph"
)

func TestWorkerGenerationsRetainSharedDrainingWorker(t *testing.T) {
	a1, a2 := new(workerProcess), new(workerProcess)
	b1, b2 := new(workerProcess), new(workerProcess)

	var generations workerGenerations
	generations.add(1, map[string]*workerProcess{"a": a1, "b": b1})
	generations.add(2, map[string]*workerProcess{"a": a2, "b": b1})
	generations.add(3, map[string]*workerProcess{"a": a2, "b": b2})
	if stopped := generations.remove(2); len(stopped) != 0 {
		t.Fatalf("retiring generation 2 would stop shared workers: %v", stopped)
	}
	if stopped := generations.remove(1); len(stopped) != 2 || !slices.Contains(stopped, a1) || !slices.Contains(stopped, b1) {
		t.Fatalf("retiring generation 1 should stop A1 and B1: %v", stopped)
	}
}

func TestWorkerFieldDefaultsUseDeclaredWireTypes(t *testing.T) {
	integer := map[string]any{"$scalar": "int", "value": "1"}
	duration := map[string]any{"$scalar": "duration", "nanoseconds": "2000000000"}
	uuid := map[string]any{"$scalar": "uuid", "value": "2a8b8956-ac42-4948-9f25-816a5f92047f"}
	decimal := map[string]any{"$scalar": "decimal", "coefficient": "125", "scale": 2}
	fields := []any{
		map[string]any{"name": "count", "type": "int64", "default": integer},
		map[string]any{"name": "delay", "type": "duration", "default": duration},
		map[string]any{"name": "id", "type": "uuid", "default": uuid},
		map[string]any{"name": "price", "type": "decimal", "default": decimal},
		map[string]any{"name": "limits", "type": "map(list(int64))", "default": map[string]any{"daily": []any{integer}}},
	}
	const address = "pricing/record/Request"
	types, err := projectWorkerTypes(&graph.Manifest{Resources: []graph.Resource{{
		Address: address, Module: "pricing", Kind: "scenery.record", Spec: map[string]any{"field": fields},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := types.Normalize(address, []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"count":"1","delay":"PT2S","id":"2a8b8956-ac42-4948-9f25-816a5f92047f","limits":{"daily":["1"]},"price":"1.25"}`
	if string(got) != want {
		t.Fatalf("normalized input = %s, want %s", got, want)
	}
}
