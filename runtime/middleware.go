package runtime

import (
	"bytes"
	"context"
	"net/http"
	"reflect"

	"pulse.dev/errs"
	pulsemiddleware "pulse.dev/middleware"
)

func executeTypedEndpoint(ep *Endpoint, ctx context.Context, pathArgs []any, payload any) (any, int, http.Header, error) {
	resp := runMiddlewareChain(ep, ctx, func(req pulsemiddleware.Request) pulsemiddleware.Response {
		callCtx := req.Context()
		if callCtx == nil {
			callCtx = context.Background()
		}
		out, err := ep.Invoke(callCtx, pathArgs, payload)
		if err != nil {
			return pulsemiddleware.Response{Err: err, HTTPStatus: errs.HTTPStatus(err)}
		}
		return pulsemiddleware.Response{Payload: out}
	})
	return finalizeTypedMiddlewareResponse(ep, resp)
}

func executeRawEndpoint(ep *Endpoint, req *http.Request) (int, http.Header, []byte, error) {
	resp := runMiddlewareChain(ep, req.Context(), func(mwReq pulsemiddleware.Request) pulsemiddleware.Response {
		capture := newRawResponseCapture()
		httpReq := req
		if ctx := mwReq.Context(); ctx != nil && ctx != req.Context() {
			httpReq = req.WithContext(ctx)
		}
		ep.RawHandler(capture, httpReq)

		resp := pulsemiddleware.Response{HTTPStatus: capture.StatusCode()}
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

func runMiddlewareChain(ep *Endpoint, ctx context.Context, leaf func(pulsemiddleware.Request) pulsemiddleware.Response) pulsemiddleware.Response {
	middlewares, err := getMiddlewares(ep.MiddlewareIDs)
	if err != nil {
		return pulsemiddleware.Response{Err: err, HTTPStatus: errs.HTTPStatus(err)}
	}
	req := pulsemiddleware.NewLazyRequest(ctx, CurrentRequest)
	if len(middlewares) == 0 {
		return leaf(req)
	}

	var counter int
	var next pulsemiddleware.Next
	next = func(req pulsemiddleware.Request) (resp pulsemiddleware.Response) {
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
					resp = pulsemiddleware.Response{
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
					resp = pulsemiddleware.Response{
						Err:        errs.B().Code(errs.Internal).Msgf("panic handling request: %v", recovered).Err(),
						HTTPStatus: http.StatusInternalServerError,
					}
				}
			}()
			return leaf(req)
		default:
			return pulsemiddleware.Response{
				Err:        errs.B().Code(errs.Internal).Msg("middleware called next() too many times").Err(),
				HTTPStatus: http.StatusInternalServerError,
			}
		}
	}

	return next(req)
}

func finalizeTypedMiddlewareResponse(ep *Endpoint, resp pulsemiddleware.Response) (any, int, http.Header, error) {
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
