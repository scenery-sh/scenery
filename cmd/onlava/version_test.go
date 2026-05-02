package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestBuildVersionResponse(t *testing.T) {
	resp := buildVersionResponse()
	if resp.SchemaVersion != "onlava.version.v1" {
		t.Fatalf("schema = %q", resp.SchemaVersion)
	}
	if resp.Version == "" {
		t.Fatal("version is empty")
	}
	if resp.GoVersion == "" {
		t.Fatal("go version is empty")
	}
}

func TestWriteVersionJSON(t *testing.T) {
	var out bytes.Buffer
	if err := writeVersionJSON(&out, buildVersionResponse()); err != nil {
		t.Fatalf("writeVersionJSON() error = %v", err)
	}
	var payload versionResponse
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != "onlava.version.v1" {
		t.Fatalf("schema = %q", payload.SchemaVersion)
	}
}
