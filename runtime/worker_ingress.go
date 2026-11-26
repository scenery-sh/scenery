package runtime

import (
	"net/http"
	"strings"
)

type workerIngressWriter struct {
	http.ResponseWriter
	started      bool
	hideIdentity bool
}

func neutralWorkerIngress(next http.Handler, development bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		// Internal headers are trusted only after dispatch sets them inside
		// the Host. Drop externally supplied X-Scenery-* spellings.
		for name := range request.Header {
			if strings.HasPrefix(strings.ToLower(name), "x-scenery-") {
				delete(request.Header, name)
			}
		}
		next.ServeHTTP(&workerIngressWriter{ResponseWriter: w, hideIdentity: !development}, request)
	})
}

func (w *workerIngressWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *workerIngressWriter) WriteHeader(status int) {
	w.neutralize()
	if status < 200 && status != http.StatusSwitchingProtocols {
		w.started = false
	}
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
	headers := w.Header()
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
		if !w.hideIdentity {
			if neutral := names[name]; neutral != "" {
				headers[http.CanonicalHeaderKey(neutral)] = values
			}
		}
		delete(headers, name)
	}
	hidden := make(map[string]bool, len(names))
	if w.hideIdentity {
		for _, neutral := range names {
			canonical := http.CanonicalHeaderKey(neutral)
			hidden[canonical] = true
			delete(headers, canonical)
		}
	}
	var expose []string
	for _, value := range headers.Values("Access-Control-Expose-Headers") {
		for name := range strings.SplitSeq(value, ",") {
			name = strings.TrimSpace(name)
			canonical := http.CanonicalHeaderKey(name)
			if alias := names[canonical]; alias != "" {
				if w.hideIdentity {
					continue
				}
				name = alias
				canonical = http.CanonicalHeaderKey(name)
			}
			if name != "" && !strings.Contains(strings.ToLower(name), "scenery") && !hidden[canonical] {
				expose = append(expose, name)
			}
		}
	}
	headers.Del("Access-Control-Expose-Headers")
	if len(expose) > 0 {
		headers.Set("Access-Control-Expose-Headers", strings.Join(expose, ", "))
	}
}
