package runtime

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
)

var (
	workerIdentityHeaders = map[string]string{
		processGenerationHeader:                           "X-App-Generation",
		http.CanonicalHeaderKey(processIdentityPIDHeader): "X-App-Host-Pid",
		processIdentityContractHdr:                        "X-App-Contract-Revision",
		processIdentityImplHeader:                         "X-App-Implementation-Revision",
		processIdentityTargetHeader:                       "X-App-Target",
		processIdentityBuildHeader:                        "X-App-Build-Digest",
		http.CanonicalHeaderKey(processServiceIDHeader):   "X-App-Worker-Pid",
		processServiceImplementationHeader:                "X-App-Worker-Revision",
	}
	productionHiddenIdentityHeaders = func() map[string]bool {
		hidden := make(map[string]bool, len(workerIdentityHeaders))
		for _, neutral := range workerIdentityHeaders {
			hidden[http.CanonicalHeaderKey(neutral)] = true
		}
		return hidden
	}()
)

type workerIngressWriter struct {
	http.ResponseWriter
	started      bool
	hideIdentity bool
}

type workerIngressRequestBody struct {
	io.ReadCloser
	mu       sync.Mutex
	scrubbed bool
	request  *http.Request
}

func (b *workerIngressRequestBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, err := b.ReadCloser.Read(p)
	if err != nil && !b.scrubbed {
		scrubInboundSceneryHeaders(b.request.Trailer)
		if err == io.EOF {
			b.scrubbed = true
		}
	}
	return n, err
}

func (b *workerIngressRequestBody) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	err := b.ReadCloser.Close()
	if !b.scrubbed {
		scrubInboundSceneryHeaders(b.request.Trailer)
		b.scrubbed = true
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
		if len(request.TransferEncoding) > 0 && request.Trailer == nil {
			// Share a pre-initialized map across req.WithContext clones so
			// unannounced chunked trailers scrubbed here stay visible downstream.
			request.Trailer = make(http.Header)
		}
		if request.Body != nil && request.Body != http.NoBody && request.ProtoMajor < 2 {
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

func (w *workerIngressWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	alreadyStarted := w.started
	w.scrubHeadersAndTrailers()
	conn, brw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err != nil {
		return nil, nil, err
	}
	w.started = true
	wrapped := &workerIngressHijackedConn{
		Conn:         conn,
		passthrough:  alreadyStarted,
		hideIdentity: w.hideIdentity,
	}
	var out net.Conn = wrapped
	if halfCloser, ok := conn.(interface{ CloseWrite() error }); ok {
		out = &workerIngressHijackedHalfCloseConn{
			workerIngressHijackedConn: wrapped,
			halfCloser:                halfCloser,
		}
	}
	if brw != nil && brw.Writer != nil {
		brw.Writer.Reset(out)
	}
	return out, brw, nil
}

const maxHijackedResponseHeaderBytes = 64 << 10

type workerIngressHijackedConn struct {
	net.Conn
	mu           sync.Mutex
	passthrough  bool
	hideIdentity bool
	buf          bytes.Buffer
}

type workerIngressHijackedHalfCloseConn struct {
	*workerIngressHijackedConn
	halfCloser interface{ CloseWrite() error }
}

func (c *workerIngressHijackedHalfCloseConn) CloseWrite() error {
	if err := c.flushUnfinishedHeader(); err != nil {
		return err
	}
	return c.halfCloser.CloseWrite()
}

func (c *workerIngressHijackedConn) Close() error {
	_ = c.flushUnfinishedHeader()
	return c.Conn.Close()
}

func (c *workerIngressHijackedConn) flushUnfinishedHeader() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.passthrough || c.buf.Len() == 0 {
		c.passthrough = true
		return nil
	}
	// Never flush an incomplete HTTP/ response header block un-scrubbed.
	data := c.buf.Bytes()
	c.buf.Reset()
	c.passthrough = true
	if bytes.HasPrefix(data, []byte("HTTP/")) {
		return errors.New("http: truncated hijacked response header")
	}
	return writeAllConn(c.Conn, data)
}

func writeAllConn(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func findHTTPHeaderEnd(data []byte) (sepIdx, sepLen int, ok bool) {
	crlfIdx := bytes.Index(data, []byte("\r\n\r\n"))
	lfIdx := bytes.Index(data, []byte("\n\n"))
	switch {
	case crlfIdx >= 0 && (lfIdx < 0 || crlfIdx < lfIdx):
		return crlfIdx, 4, true
	case lfIdx >= 0:
		return lfIdx, 2, true
	default:
		return 0, 0, false
	}
}

func (c *workerIngressHijackedConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.passthrough {
		return c.Conn.Write(p)
	}
	c.buf.Write(p)
	for {
		data := c.buf.Bytes()
		if len(data) == 0 {
			return len(p), nil
		}
		if (len(data) < 5 && !strings.HasPrefix("HTTP/", string(data))) ||
			(len(data) >= 5 && !bytes.HasPrefix(data, []byte("HTTP/"))) {
			c.passthrough = true
			c.buf.Reset()
			if err := writeAllConn(c.Conn, data); err != nil {
				return 0, err
			}
			return len(p), nil
		}
		_, sepLen, ok := findHTTPHeaderEnd(data)
		if !ok {
			if len(data) > maxHijackedResponseHeaderBytes {
				c.buf.Reset()
				c.passthrough = true
				return 0, errors.New("http: hijacked response header too large")
			}
			return len(p), nil
		}
		headerEnd := bytes.Index(data, []byte("\r\n\r\n"))
		if sepLen == 2 {
			headerEnd = bytes.Index(data, []byte("\n\n"))
		}
		headerEnd += sepLen
		rest := bytes.Clone(data[headerEnd:])
		tp := textproto.NewReader(bufio.NewReader(bytes.NewReader(data[:headerEnd])))
		statusLine, err := tp.ReadLine()
		if err != nil {
			c.buf.Reset()
			c.passthrough = true
			return 0, err
		}
		mimeHdr, err := tp.ReadMIMEHeader()
		if err != nil {
			c.buf.Reset()
			c.passthrough = true
			return 0, err
		}
		proto, afterProto, hasProto := strings.Cut(statusLine, " ")
		codeStr, _, _ := strings.Cut(afterProto, " ")
		code, codeErr := strconv.Atoi(codeStr)
		if !hasProto || codeErr != nil {
			c.buf.Reset()
			c.passthrough = true
			return 0, errors.New("http: invalid hijacked response status line")
		}
		if strings.Contains(strings.ToLower(statusLine), "scenery") {
			if reason := http.StatusText(code); reason != "" {
				statusLine = proto + " " + codeStr + " " + reason
			} else {
				statusLine = proto + " " + codeStr
			}
		}
		headers := http.Header(mimeHdr)
		scrubResponseHeaderMap(headers, c.hideIdentity)

		var out bytes.Buffer
		out.WriteString(statusLine)
		out.WriteString("\r\n")
		if err := headers.Write(&out); err != nil {
			c.buf.Reset()
			c.passthrough = true
			return 0, err
		}
		out.WriteString("\r\n")
		if code >= 100 && code < 200 && code != http.StatusSwitchingProtocols {
			if err := writeAllConn(c.Conn, out.Bytes()); err != nil {
				c.buf.Reset()
				c.passthrough = true
				return 0, err
			}
			c.buf.Reset()
			c.buf.Write(rest)
			continue
		}
		out.Write(rest)
		c.buf.Reset()
		c.passthrough = true
		if err := writeAllConn(c.Conn, out.Bytes()); err != nil {
			return 0, err
		}
		return len(p), nil
	}
}

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

var headerNameListFields = [...]string{
	"Access-Control-Expose-Headers",
	"Access-Control-Allow-Headers",
	"Trailer",
	"Connection",
	"Vary",
}

func isHeaderNameListField(canonical string) bool {
	for _, field := range headerNameListFields {
		if canonical == field {
			return true
		}
	}
	return false
}

func normalizeHeaderListToken(name string) string {
	switch strings.ToLower(name) {
	case "*":
		return "*"
	case "close":
		return "close"
	case "keep-alive":
		return "keep-alive"
	default:
		return http.CanonicalHeaderKey(name)
	}
}

func filterHeaderNameList(values []string, names map[string]string, hidden map[string]bool, hideIdentity bool) []string {
	var allowed []string
	for _, value := range values {
		for name := range strings.SplitSeq(value, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			canonical := normalizeHeaderListToken(name)
			if alias := names[http.CanonicalHeaderKey(name)]; alias != "" {
				if hideIdentity {
					continue
				}
				name = alias
				canonical = normalizeHeaderListToken(name)
			}
			if !strings.Contains(strings.ToLower(name), "scenery") && !hidden[http.CanonicalHeaderKey(name)] && !hidden[canonical] {
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
	scrubResponseHeaderMap(w.Header(), w.hideIdentity)
}

func scrubResponseHeaderMap(headers http.Header, hideIdentity bool) {
	names := workerIdentityHeaders
	var hidden map[string]bool
	if hideIdentity {
		hidden = productionHiddenIdentityHeaders
	}
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
				if !hideIdentity {
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
			if !hideIdentity {
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
		if allowed := filterHeaderNameList(values, names, hidden, hideIdentity); len(allowed) > 0 {
			rewrites = append(rewrites, headerRewrite{key: http.TrailerPrefix + canonical, values: []string{strings.Join(allowed, ", ")}})
		}
	}
	for _, rewrite := range rewrites {
		headers[rewrite.key] = rewrite.values
	}
	for _, field := range headerNameListFields {
		scrubHeaderNameList(headers, field, names, hidden, hideIdentity)
	}
}
