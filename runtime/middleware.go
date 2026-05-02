package runtime

import (
	"bytes"
	"context"
	"net/http"
	"reflect"

	"onlava.com/errs"
	onlavamiddleware "onlava.com/middleware"
)

func executeTypedEndpoint(ep *Endpoint, ctx context.Context, pathArgs []any, payload any) (any, int, http.Header, error) {
	resp := runMiddlewareChain(ep, ctx, func(req onlavamiddleware.Request) onlavamiddleware.Response {
		callCtx := req.Context()
		if callCtx == nil {
			callCtx = context.Background()
		}
		out, mocked, err := invokeTypedEndpointMock(ep, callCtx, pathArgs, payload)
		if !mocked {
			out, err = ep.Invoke(callCtx, pathArgs, payload)
		}
		if err != nil {
			return onlavamiddleware.Response{Err: err, HTTPStatus: errs.HTTPStatus(err)}
		}
		return onlavamiddleware.Response{Payload: out}
	})
	return finalizeTypedMiddlewareResponse(ep, resp)
}

func executeRawEndpoint(ep *Endpoint, req *http.Request) (int, http.Header, []byte, error) {
	resp := runMiddlewareChain(ep, req.Context(), func(mwReq onlavamiddleware.Request) onlavamiddleware.Response {
		capture := newRawResponseCapture()
		httpReq := req
		if ctx := mwReq.Context(); ctx != nil && ctx != req.Context() {
			httpReq = req.WithContext(ctx)
		}
		if mocked, err := invokeRawEndpointMock(ep, capture, httpReq); mocked {
			if err != nil {
				return onlavamiddleware.Response{Err: err, HTTPStatus: errs.HTTPStatus(err)}
			}
		} else {
			ep.RawHandler(capture, httpReq)
		}

		resp := onlavamiddleware.Response{HTTPStatus: capture.StatusCode()}
		copyHeaders(resp.Header(), capture.Header())
		resp.Payload = rawMiddlewarePayload{body: append([]byte(nil), capture.body.Bytes()...)}
		return resp
	})
	headers := cloneHeaders(resp.GetHeaders())
	if resp.Err != nil {
		return resp.HTTPStatus, headers, nil, resp.Err
	}
	body := []byte(nil)
	switch payload := resp.Payload.(type) {
	case rawMiddlewarePayload:
		body = payload.body
	case *rawMiddlewarePayload:
		if payload != nil {
			body = payload.body
		}
	}
	return resp.HTTPStatus, headers, body, nil
}

func runMiddlewareChain(ep *Endpoint, ctx context.Context, leaf func(onlavamiddleware.Request) onlavamiddleware.Response) onlavamiddleware.Response {
	middlewares, err := getMiddlewares(ep.MiddlewareIDs)
	if err != nil {
		return onlavamiddleware.Response{Err: err, HTTPStatus: errs.HTTPStatus(err)}
	}
	req := onlavamiddleware.NewLazyRequest(ctx, CurrentRequest)
	if len(middlewares) == 0 {
		return leaf(req)
	}

	var counter int
	var next onlavamiddleware.Next
	next = func(req onlavamiddleware.Request) (resp onlavamiddleware.Response) {
		defer func() {
			if resp.HTTPStatus == 0 && resp.Err != nil {
				resp.HTTPStatus = errs.HTTPStatus(resp.Err)
			}
		}()

		idx := counter
		counter++
		switch {
		case idx < len(middlewares):
			mw := middlewares[idx]
			recordMiddlewareEvent(mw.ID, "start", nil)
			defer func() {
				if recovered := recover(); recovered != nil {
					resp = onlavamiddleware.Response{
						Err:        errs.B().Code(errs.Internal).Msgf("panic executing middleware %s: %v", mw.ID, recovered).Err(),
						HTTPStatus: http.StatusInternalServerError,
					}
					recordMiddlewareEvent(mw.ID, "panic", resp.Err)
				}
				recordMiddlewareEvent(mw.ID, "end", resp.Err)
			}()
			return mw.Invoke(req, next)
		case idx == len(middlewares):
			defer func() {
				if recovered := recover(); recovered != nil {
					resp = onlavamiddleware.Response{
						Err:        errs.B().Code(errs.Internal).Msgf("panic handling request: %v", recovered).Err(),
						HTTPStatus: http.StatusInternalServerError,
					}
				}
			}()
			return leaf(req)
		default:
			return onlavamiddleware.Response{
				Err:        errs.B().Code(errs.Internal).Msg("middleware called next() too many times").Err(),
				HTTPStatus: http.StatusInternalServerError,
			}
		}
	}

	return next(req)
}

func finalizeTypedMiddlewareResponse(ep *Endpoint, resp onlavamiddleware.Response) (any, int, http.Header, error) {
	headers := cloneHeaders(resp.GetHeaders())
	if resp.Err != nil {
		return nil, resp.HTTPStatus, headers, resp.Err
	}
	if ep.ResponseType == nil {
		if resp.Payload != nil {
			return nil, http.StatusInternalServerError, headers, errs.B().Code(errs.Internal).Msgf("invalid middleware: endpoint %s.%s cannot return a payload", ep.Service, ep.Name).Err()
		}
		return nil, resp.HTTPStatus, headers, nil
	}
	if resp.Payload == nil {
		return nil, resp.HTTPStatus, headers, nil
	}
	payloadType := reflect.TypeOf(resp.Payload)
	if !payloadType.AssignableTo(ep.ResponseType) {
		return nil, http.StatusInternalServerError, headers, errs.B().Code(errs.Internal).Msgf(
			"invalid middleware: cannot return payload of type %T for endpoint %s.%s (expected %s)",
			resp.Payload, ep.Service, ep.Name, ep.ResponseType,
		).Err()
	}
	return resp.Payload, resp.HTTPStatus, headers, nil
}

func applyHeaders(dst, src http.Header) {
	for key := range src {
		dst.Del(key)
	}
	copyHeaders(dst, src)
}

func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		dst[key] = append([]string(nil), values...)
	}
}

func cloneHeaders(headers http.Header) http.Header {
	if headers == nil {
		return nil
	}
	clone := make(http.Header, len(headers))
	copyHeaders(clone, headers)
	return clone
}

type rawMiddlewarePayload struct {
	body []byte
}

func canStreamRawEndpoint(ep *Endpoint) bool {
	if ep == nil || len(ep.MiddlewareIDs) != 0 {
		return false
	}
	_, mocked := lookupEndpointMock(ep)
	return !mocked
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

func (r *rawStreamingResponseWriter) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func (r *rawStreamingResponseWriter) StatusCode() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
}

func (r *rawStreamingResponseWriter) WroteHeader() bool {
	return r.wroteHeader
}

type rawResponseCapture struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func newRawResponseCapture() *rawResponseCapture {
	return &rawResponseCapture{header: make(http.Header)}
}

func (r *rawResponseCapture) Header() http.Header {
	return r.header
}

func (r *rawResponseCapture) Write(data []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(data)
}

func (r *rawResponseCapture) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}

func (r *rawResponseCapture) Flush() {}

func (r *rawResponseCapture) StatusCode() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
}
