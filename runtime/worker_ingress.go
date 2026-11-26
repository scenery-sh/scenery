package runtime

import (
	"io"
	"net/http"
	"strings"
)

type workerIngressWriter struct {
	http.ResponseWriter
	started      bool
	hideIdentity bool
}

type workerIngressRequestBody struct {
	io.ReadCloser
	trailer http.Header
}

func (b *workerIngressRequestBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		scrubInboundSceneryHeaders(b.trailer)
	}
	return n, err
}

func neutralWorkerIngress(next http.Handler, development bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		// Internal headers are trusted only after dispatch sets them inside
		// the Host. Drop externally supplied X-Scenery-* spellings.
		scrubInboundSceneryHeaders(request.Header)
		scrubInboundSceneryTrailerHeader(request.Header)
		scrubInboundSceneryHeaders(request.Trailer)
		if request.Body != nil && len(request.Trailer) > 0 {
			request.Body = &workerIngressRequestBody{ReadCloser: request.Body, trailer: request.Trailer}
		}
		writer := &workerIngressWriter{ResponseWriter: w, hideIdentity: !development}
		defer writer.scrubHeadersAndTrailers()
		next.ServeHTTP(writer, request)
	})
}

func scrubInboundSceneryHeaders(headers http.Header) {
	for name := range headers {
		if strings.HasPrefix(strings.ToLower(name), "x-scenery-") {
			delete(headers, name)
		}
	}
}

func scrubInboundSceneryTrailerHeader(headers http.Header) {
	var allowed []string
	for _, value := range headers.Values("Trailer") {
		for name := range strings.SplitSeq(value, ",") {
			name = strings.TrimSpace(name)
			if name == "" || strings.HasPrefix(strings.ToLower(name), "x-scenery-") {
				continue
			}
			allowed = append(allowed, http.CanonicalHeaderKey(name))
		}
	}
	headers.Del("Trailer")
	if len(allowed) > 0 {
		headers.Set("Trailer", strings.Join(allowed, ", "))
	}
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
	if !w.started {
		w.neutralize()
	} else {
		w.scrubHeadersAndTrailers()
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *workerIngressWriter) neutralize() {
	if w.started {
		return
	}
	w.scrubHeadersAndTrailers()
	w.started = true
}

func workerIdentityHeaderNames() map[string]string {
	return map[string]string{
		processGenerationHeader:                           "X-App-Generation",
		http.CanonicalHeaderKey(processIdentityPIDHeader): "X-App-Host-Pid",
		processIdentityContractHdr:                        "X-App-Contract-Revision",
		processIdentityImplHeader:                         "X-App-Implementation-Revision",
		processIdentityTargetHeader:                       "X-App-Target",
		processIdentityBuildHeader:                        "X-App-Build-Digest",
		http.CanonicalHeaderKey(processServiceIDHeader):   "X-App-Worker-Pid",
		processServiceImplementationHeader:                "X-App-Worker-Revision",
	}
}

func hiddenIdentityHeaders(names map[string]string, hideIdentity bool) map[string]bool {
	hidden := make(map[string]bool, len(names))
	if !hideIdentity {
		return hidden
	}
	for _, neutral := range names {
		hidden[http.CanonicalHeaderKey(neutral)] = true
	}
	return hidden
}

func scrubHeaderNameList(headers http.Header, listHeader string, names map[string]string, hidden map[string]bool, hideIdentity bool) {
	var allowed []string
	for _, value := range headers.Values(listHeader) {
		for name := range strings.SplitSeq(value, ",") {
			name = strings.TrimSpace(name)
			canonical := http.CanonicalHeaderKey(name)
			if alias := names[canonical]; alias != "" {
				if hideIdentity {
					continue
				}
				name = alias
				canonical = http.CanonicalHeaderKey(name)
			}
			if name != "" && !strings.Contains(strings.ToLower(name), "scenery") && !hidden[canonical] {
				allowed = append(allowed, canonical)
			}
		}
	}
	headers.Del(listHeader)
	if len(allowed) > 0 {
		headers.Set(listHeader, strings.Join(allowed, ", "))
	}
}

func cutTrailerPrefix(name string) (string, bool) {
	if len(name) > len(http.TrailerPrefix) && strings.EqualFold(name[:len(http.TrailerPrefix)], http.TrailerPrefix) {
		return name[len(http.TrailerPrefix):], true
	}
	return "", false
}

func (w *workerIngressWriter) scrubHeadersAndTrailers() {
	headers := w.Header()
	names := workerIdentityHeaderNames()
	hidden := hiddenIdentityHeaders(names, w.hideIdentity)
	type headerRewrite struct {
		key    string
		values []string
	}
	var rewrites []headerRewrite
	for name, values := range headers {
		if rawTrailer, isTrailer := cutTrailerPrefix(name); isTrailer {
			canonical := http.CanonicalHeaderKey(rawTrailer)
			if alias := names[canonical]; alias != "" {
				if !w.hideIdentity {
					rewrites = append(rewrites, headerRewrite{key: http.TrailerPrefix + http.CanonicalHeaderKey(alias), values: values})
				}
				delete(headers, name)
				continue
			}
			if strings.Contains(strings.ToLower(rawTrailer), "scenery") || hidden[canonical] {
				delete(headers, name)
			}
			continue
		}
		canonical := http.CanonicalHeaderKey(name)
		if strings.Contains(strings.ToLower(name), "scenery") {
			if !w.hideIdentity {
				if neutral := names[canonical]; neutral != "" {
					rewrites = append(rewrites, headerRewrite{key: http.CanonicalHeaderKey(neutral), values: values})
				}
			}
			delete(headers, name)
			continue
		}
		if hidden[canonical] {
			delete(headers, name)
		}
	}
	for _, rewrite := range rewrites {
		headers[rewrite.key] = rewrite.values
	}
	if !w.started {
		scrubHeaderNameList(headers, "Access-Control-Expose-Headers", names, hidden, w.hideIdentity)
		scrubHeaderNameList(headers, "Trailer", names, hidden, w.hideIdentity)
	}
}
