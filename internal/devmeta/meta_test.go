package devmeta

import (
	"encoding/json"
	"testing"

	"onlava.com/internal/model"
)

func TestBuildMetadataSnapshotIncludesPlatformStats(t *testing.T) {
	metaJSON, err := BuildMetadataSnapshot(&model.App{})
	if err != nil {
		t.Fatalf("BuildMetadataSnapshot() error = %v", err)
	}

	var payload struct {
		Services []struct {
			Name string `json:"name"`
			RPCs []struct {
				Name        string   `json:"name"`
				AccessType  string   `json:"access_type"`
				HTTPMethods []string `json:"http_methods"`
			} `json:"rpcs"`
		} `json:"svcs"`
	}
	if err := json.Unmarshal(metaJSON, &payload); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}

	for _, svc := range payload.Services {
		if svc.Name != "platform" {
			continue
		}
		for _, rpc := range svc.RPCs {
			if rpc.Name != "Stats" {
				continue
			}
			if rpc.AccessType != "public" {
				t.Fatalf("access_type = %q, want public", rpc.AccessType)
			}
			if len(rpc.HTTPMethods) != 1 || rpc.HTTPMethods[0] != "GET" {
				t.Fatalf("http_methods = %v, want [GET]", rpc.HTTPMethods)
			}
			return
		}
	}
	t.Fatal("platform.Stats metadata missing")
}

func TestBuildAPIEncodingIncludesPlatformStats(t *testing.T) {
	apiJSON, err := BuildAPIEncoding(&model.App{})
	if err != nil {
		t.Fatalf("BuildAPIEncoding() error = %v", err)
	}

	var payload struct {
		Services []struct {
			Name string `json:"name"`
			RPCs []struct {
				Name    string   `json:"name"`
				Path    string   `json:"path"`
				Methods []string `json:"methods"`
			} `json:"rpcs"`
		} `json:"services"`
	}
	if err := json.Unmarshal(apiJSON, &payload); err != nil {
		t.Fatalf("decode api encoding: %v", err)
	}

	for _, svc := range payload.Services {
		if svc.Name != "platform" {
			continue
		}
		for _, rpc := range svc.RPCs {
			if rpc.Name != "Stats" {
				continue
			}
			if rpc.Path != "/platform.Stats" {
				t.Fatalf("path = %q, want /platform.Stats", rpc.Path)
			}
			if len(rpc.Methods) != 1 || rpc.Methods[0] != "GET" {
				t.Fatalf("methods = %v, want [GET]", rpc.Methods)
			}
			return
		}
	}
	t.Fatal("platform.Stats API encoding missing")
}
