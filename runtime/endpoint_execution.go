package runtime

import (
	"context"
	"net/http"

	"scenery.sh/errs"
)

func executeTypedEndpoint(ep *Endpoint, ctx context.Context, pathArgs []any, payload any) (any, int, http.Header, error) {
	out, err := invokeContractPipeline(ctx, ep.ContractPolicy, func(invocationCtx context.Context) (any, error) {
		return ep.Invoke(invocationCtx, pathArgs, payload)
	})
	if err == nil {
		return out, 0, nil, nil
	}
	status := errs.HTTPStatus(err)
	if transportStatus, ok := contractTransportHTTPStatus(err); ok {
		status = transportStatus
	}
	return nil, status, nil, err
}

func applyHeaders(dst, src http.Header) {
	for key := range src {
		dst.Del(key)
	}
	for key, values := range src {
		dst[key] = append([]string(nil), values...)
	}
}

func executeStreamingRawEndpoint(ep *Endpoint, w http.ResponseWriter, req *http.Request) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = errs.B().Code(errs.Internal).Msgf("panic handling request: %v", recovered).Err()
		}
	}()
	ep.RawHandler(w, req)
	return nil
}

type rawStreamingResponseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func newRawStreamingResponseWriter(w http.ResponseWriter) *rawStreamingResponseWriter {
	return &rawStreamingResponseWriter{ResponseWriter: w}
}

func (r *rawStreamingResponseWriter) Write(data []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(data)
}

func (r *rawStreamingResponseWriter) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.status = status
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *rawStreamingResponseWriter) Flush() {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *rawStreamingResponseWriter) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *rawStreamingResponseWriter) StatusCode() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
}

func (r *rawStreamingResponseWriter) WroteHeader() bool { return r.wroteHeader }
