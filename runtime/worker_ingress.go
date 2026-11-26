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
	request *http.Request
}

func (b *workerIngressRequestBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if len(b.request.Trailer) > 0 {
		scrubInboundSceneryHeaders(b.request.Trailer)
	}
	return n, err
}

func (b *workerIngressRequestBody) Close() error {
	err := b.ReadCloser.Close()
	if len(b.request.Trailer) > 0 {
		scrubInboundSceneryHeaders(b.request.Trailer)
	}
	return err
}

func neutralWorkerIngress(next http.Handler, development bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		// Internal headers are trusted only after dispatch sets them inside
		// the Host. Drop externally supplied X-Scenery-* spellings.
		scrubInboundSceneryHeaders(request.Header)
		scrubInboundSceneryTrailerHeader(request.Header)
		scrubInboundSceneryHeaders(request.Trailer)
		if request.Body != nil && request.Body != http.NoBody {
			request.Body = &workerIngressRequestBody{ReadCloser: request.Body, request: request}
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
	for name, values := range headers {
		if !strings.EqualFold(name, "Trailer") {
			continue
		}
		for _, value := range values {
			for item := range strings.SplitSeq(value, ",") {
				item = strings.TrimSpace(item)
				if item == "" || strings.HasPrefix(strings.ToLower(item), "x-scenery-") {
					continue
				}
				allowed = append(allowed, http.CanonicalHeaderKey(item))
			}
		}
		delete(headers, name)
	}
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

func isHeaderNameListField(canonical string) bool {
	return canonical == "Access-Control-Expose-Headers" || canonical == "Trailer"
}

func filterHeaderNameList(values []string, names map[string]string, hidden map[string]bool, hideIdentity bool) []string {
	var allowed []string
	for _, value := range values {
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
	return allowed
}

func scrubHeaderNameList(headers http.Header, listHeader string, names map[string]string, hidden map[string]bool, hideIdentity bool) {
	var values []string
	for name, existing := range headers {
		if strings.EqualFold(name, listHeader) {
			values = append(values, existing...)
			delete(headers, name)
		}
	}
	if allowed := filterHeaderNameList(values, names, hidden, hideIdentity); len(allowed) > 0 {
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
	listTrailerValues := make(map[string][]string)
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
				continue
			}
			if isHeaderNameListField(canonical) {
				listTrailerValues[canonical] = append(listTrailerValues[canonical], values...)
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
	for canonical, values := range listTrailerValues {
		if allowed := filterHeaderNameList(values, names, hidden, w.hideIdentity); len(allowed) > 0 {
			rewrites = append(rewrites, headerRewrite{key: http.TrailerPrefix + canonical, values: []string{strings.Join(allowed, ", ")}})
		}
	}
	for _, rewrite := range rewrites {
		headers[rewrite.key] = rewrite.values
	}
	scrubHeaderNameList(headers, "Access-Control-Expose-Headers", names, hidden, w.hideIdentity)
	scrubHeaderNameList(headers, "Trailer", names, hidden, w.hideIdentity)
}
