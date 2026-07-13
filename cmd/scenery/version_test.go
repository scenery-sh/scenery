package main

import (
	"bytes"
	"testing"
)

func TestBuildVersionResponse(t *testing.T) {
	t.Parallel()

	resp := buildVersionResponse()
	if resp.Kind != "scenery.version" || resp.SchemaRevision != newCLIPayloadIdentity("scenery.version").SchemaRevision {
		t.Fatalf("identity = %q %q", resp.Kind, resp.SchemaRevision)
	}
	if resp.Version == "" {
		t.Fatal("version is empty")
	}
	if resp.GoVersion == "" {
		t.Fatal("go version is empty")
	}
}

func TestWriteVersionJSON(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := writeVersionJSON(&out, buildVersionResponse()); err != nil {
		t.Fatalf("writeVersionJSON() error = %v", err)
	}
	var payload versionResponse
	if err := decodeCLIJSON(out.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	if payload.Kind != "scenery.version" || payload.SchemaRevision != newCLIPayloadIdentity("scenery.version").SchemaRevision {
		t.Fatalf("identity = %q %q", payload.Kind, payload.SchemaRevision)
	}
}
