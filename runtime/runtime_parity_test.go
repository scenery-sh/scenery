package runtime

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"scenery.sh/errs"
)

func TestRuntimeParityV1TransportErrors(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "internal", "contract", "testdata", "runtime-parity-v1.json"))
	if err != nil {
		t.Fatalf("read runtime-parity-v1.json: %v", err)
	}
	var corpus struct {
		TransportErrors []struct {
			Name         string `json:"name"`
			Status       int    `json:"status"`
			Body         string `json:"body"`
			WantCode     string `json:"want_code"`
			WantDelivery string `json:"want_delivery"`
		} `json:"transport_errors"`
	}
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatalf("decode runtime-parity-v1.json: %v", err)
	}

	for _, tc := range corpus.TransportErrors {
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.Status)
				_, _ = w.Write([]byte(tc.Body))
			}))
			t.Cleanup(server.Close)

			addr := server.Listener.Addr().String()
			transport := &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
				},
			}
			t.Cleanup(transport.CloseIdleConnections)

			ctx := context.WithValue(t.Context(), workerCapabilitiesKey{}, &workerCapabilities{
				scope:     "parity-scope",
				token:     "parity-token",
				transport: transport,
			})
			_, callErr := callWorkerCapability(ctx, http.MethodPost, "events/emit", map[string]any{"publisher": "p"})
			if callErr == nil {
				t.Fatalf("expected error for HTTP %d", tc.Status)
			}
			scnErr, ok := errs.As(callErr)
			if !ok {
				t.Fatalf("expected *errs.Error, got %T (%v)", callErr, callErr)
			}
			if string(scnErr.Code) != tc.WantCode {
				t.Fatalf("code = %q, want %q", scnErr.Code, tc.WantCode)
			}
			if scnErr.Code == errs.Unavailable {
				if got, _ := scnErr.Meta["delivery"].(string); got != tc.WantDelivery {
					t.Fatalf("delivery = %q, want %q", got, tc.WantDelivery)
				}
			}
		})
	}
}
