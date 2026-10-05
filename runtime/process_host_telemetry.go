package runtime

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"scenery.sh/internal/devtelemetry"
)

// processHostResponseWriter observes committed, attested response headers.
// Control calls, 1xx responses, proxy failures and answers of another pinned
// generation cannot satisfy the generation's first-response measurement.
type processHostResponseWriter struct {
	http.ResponseWriter
	generation *processHostGeneration
	committed  bool
}

func (w *processHostResponseWriter) WriteHeader(status int) {
	w.ResponseWriter.WriteHeader(status)
	if status < 200 || w.committed {
		return
	}
	w.committed = true
	g := w.generation
	if g == nil || g.observation.OperationID == "" || g.observation.ObservedAt.IsZero() || g.telemetry == nil ||
		w.Header().Get(processGenerationHeader) != strconv.FormatUint(g.number, 10) ||
		w.Header().Get(processIdentityBuildHeader) != g.identity.BuildInputDigest || status >= 500 || !g.firstResponse.CompareAndSwap(false, true) {
		return
	}
	event := devtelemetry.FirstResponse{Observation: g.observation, At: time.Now().UTC(), Generation: g.number,
		ImplementationRevision: g.identity.ImplementationRevision, BuildInputDigest: g.identity.BuildInputDigest, Status: status}
	encoded, err := json.Marshal(event)
	if err == nil {
		_, _ = fmt.Fprintf(g.telemetry, "%s%s\n", devtelemetry.FirstResponsePrefix, encoded)
	}
}

func (w *processHostResponseWriter) Write(data []byte) (int, error) {
	if !w.committed {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func (w *processHostResponseWriter) Flush() {
	if !w.committed {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *processHostResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
