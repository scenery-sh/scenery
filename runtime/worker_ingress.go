package runtime

import (
	"net/http"
	"strings"
)

type workerIngressWriter struct {
	http.ResponseWriter
	started bool
}

func neutralWorkerIngress(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		// Internal generation headers are trusted only after dispatch has
		// selected a generation. Drop externally supplied spellings.
		request.Header.Del(processGenerationHeader)
		next.ServeHTTP(&workerIngressWriter{ResponseWriter: w}, request)
	})
}

func (w *workerIngressWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *workerIngressWriter) WriteHeader(status int) {
	w.neutralize()
	w.ResponseWriter.WriteHeader(status)
}

func (w *workerIngressWriter) Write(data []byte) (int, error) {
	w.neutralize()
	return w.ResponseWriter.Write(data)
}

func (w *workerIngressWriter) Flush() {
	w.neutralize()
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *workerIngressWriter) neutralize() {
	if w.started {
		return
	}
	w.started = true
	headers := w.ResponseWriter.Header()
	names := map[string]string{
		processGenerationHeader: "X-App-Generation", http.CanonicalHeaderKey(processIdentityPIDHeader): "X-App-Host-Pid",
		processIdentityContractHdr: "X-App-Contract-Revision", processIdentityImplHeader: "X-App-Implementation-Revision",
		processIdentityTargetHeader: "X-App-Target", processIdentityBuildHeader: "X-App-Build-Digest",
		http.CanonicalHeaderKey(processServiceIDHeader): "X-App-Worker-Pid", processServiceImplementationHeader: "X-App-Worker-Revision",
	}
	for name, values := range headers {
		if !strings.Contains(strings.ToLower(name), "scenery") {
			continue
		}
		if neutral := names[name]; neutral != "" {
			headers[http.CanonicalHeaderKey(neutral)] = values
		}
		delete(headers, name)
	}
	var expose []string
	for _, value := range headers.Values("Access-Control-Expose-Headers") {
		for name := range strings.SplitSeq(value, ",") {
			name = strings.TrimSpace(name)
			if alias := names[http.CanonicalHeaderKey(name)]; alias != "" {
				name = alias
			}
			if name != "" && !strings.Contains(strings.ToLower(name), "scenery") {
				expose = append(expose, name)
			}
		}
	}
	headers.Del("Access-Control-Expose-Headers")
	if len(expose) > 0 {
		headers.Set("Access-Control-Expose-Headers", strings.Join(expose, ", "))
	}
}
