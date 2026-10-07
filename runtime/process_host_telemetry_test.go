package runtime

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/devtelemetry"
)

func TestFirstResponseRequiresServingIdentityAndEmitsOnce(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	generation := &processHostGeneration{number: 2, identity: processInstanceIdentity{BuildInputDigest: "new-build", ImplementationRevision: "new-implementation"}, telemetry: &output,
		observation: devtelemetry.Observation{OperationID: "build-2", ObservedAt: time.Now().Add(-time.Second)}}
	for _, response := range []struct {
		generation, build string
		status            int
	}{
		{"2", "new-build", 503}, {"1", "new-build", 200}, {"2", "old-build", 200}, {"2", "new-build", 200}, {"2", "new-build", 201},
	} {
		writer := &processHostResponseWriter{ResponseWriter: httptest.NewRecorder(), generation: generation}
		writer.Header().Set(processGenerationHeader, response.generation)
		writer.Header().Set(processIdentityBuildHeader, response.build)
		writer.WriteHeader(response.status)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("events = %q", output.String())
	}
	var event devtelemetry.FirstResponse
	if err := json.Unmarshal([]byte(strings.TrimPrefix(lines[0], devtelemetry.FirstResponsePrefix)), &event); err != nil {
		t.Fatal(err)
	}
	if event.OperationID != "build-2" || event.Generation != 2 || event.Status != http.StatusOK || event.At.Before(event.ObservedAt) || event.BuildInputDigest != "new-build" {
		t.Fatalf("event = %+v", event)
	}
}
