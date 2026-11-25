package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"scenery.sh/internal/workerabi"
	"scenery.sh/runtime/shared"
)

// The Host describes the admitted HTTP request in the invocation metadata. A
// Go handler reads it through CurrentRequest, as it does in process.
func TestGoWorkerRestoresIngressRequest(t *testing.T) {
	defer replaceGlobalRegistryForTest()()
	const operation = "orders/operation/update"
	var observed shared.Request
	if err := RegisterWorkerOperation(operation, func(context.Context, []byte) ([]byte, shared.ByteStream, error) {
		observed = *CurrentRequest()
		return []byte(`{"kind":"result","name":"ok","value":{}}`), shared.ByteStream{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err := invokeGoWorker(t.Context(), workerabi.Invocation{
		Service: "orders/service/api", Operation: operation, Mode: "direct", Input: json.RawMessage(`{}`),
		Context: workerabi.InvocationContext{InvocationID: "call", Metadata: json.RawMessage(`{
			"request_type": "api-call", "method": "PUT", "path": "/orders/42/lines/7",
			"path_params": [{"name": "order", "value": "42"}, {"name": "line", "value": "7"}],
			"headers": {"x-correlation-id": ["corr-1"], "Accept-Language": ["cs", "en"]}
		}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantParams := shared.PathParams{{Name: "order", Value: "42"}, {Name: "line", Value: "7"}}
	if observed.Type != shared.APICall || observed.Method != http.MethodPut || observed.Path != "/orders/42/lines/7" ||
		!slices.Equal(observed.PathParams, wantParams) || observed.PathParams.Get("line") != "7" ||
		observed.Headers.Get("X-Correlation-Id") != "corr-1" || !slices.Equal(observed.Headers.Values("Accept-Language"), []string{"cs", "en"}) {
		t.Fatalf("request observed by the Go worker: %+v", observed)
	}
}
