package runtime

import (
	"context"
	"net/http"
	"reflect"
	"strings"

	"github.com/julienschmidt/httprouter"

	"pulse.dev/errs"
	"pulse.dev/runtime/shared"
)

type server struct {
	public  *httprouter.Router
	private *httprouter.Router
	http    *http.Server
}

func newServer(listenAddr string) (*http.Server, error) {
	s := &server{
		public:  httprouter.New(),
		private: httprouter.New(),
	}
	s.public.NotFound = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		errs.HTTPError(w, errs.B().Code(errs.NotFound).Msg("endpoint not found").Err())
	})

	for _, ep := range listEndpoints() {
		if ep.Raw {
			s.registerRaw(ep)
			continue
		}
		s.registerTyped(ep)
	}

	httpServer := &http.Server{
		Addr:    listenAddr,
		Handler: s.public,
	}
	s.http = httpServer
	return httpServer, nil
}

func (s *server) registerRaw(ep *Endpoint) {
	handler := func(w http.ResponseWriter, req *http.Request, params httprouter.Params) {
		pathParams := make(shared.PathParams, 0, len(params))
		for _, param := range params {
			pathParams = append(pathParams, shared.PathParam{Name: param.Key, Value: strings.TrimPrefix(param.Value, "/")})
		}

		authInfo, err := authenticateRequest(req, ep)
		if err != nil {
			errs.HTTPError(w, err)
			return
		}

		state := newExternalState(ep, req, pathParams, nil, authInfo)
		ctx := withState(req.Context(), state)
		restore := enterState(state)
		defer restore()

		req = req.WithContext(ctx)
		ep.RawHandler(w, req)
	}

	registerRoute(s.selectRouter(ep), ep.Path, ep.Methods, handler)
}

func (s *server) registerTyped(ep *Endpoint) {
	handler := func(w http.ResponseWriter, req *http.Request, params httprouter.Params) {
		pathValues, pathParams, err := decodePathParams(ep, params)
		if err != nil {
			errs.HTTPError(w, err)
			return
		}

		authInfo, err := authenticateRequest(req, ep)
		if err != nil {
			errs.HTTPError(w, err)
			return
		}

		payload, err := decodePayload(req, ep.PayloadType)
		if err != nil {
			errs.HTTPError(w, err)
			return
		}

		state := newExternalState(ep, req, pathParams, payload, authInfo)
		ctx := withState(req.Context(), state)
		restore := enterState(state)
		defer restore()

		resp, callErr := ep.Invoke(ctx, pathValues, payload)
		if callErr != nil {
			errs.HTTPError(w, callErr)
			return
		}
		if err := encodeResponse(w, resp); err != nil {
			errs.HTTPError(w, errs.Wrap(err, "encode response"))
			return
		}
	}

	registerRoute(s.selectRouter(ep), ep.Path, ep.Methods, handler)
}

func (s *server) selectRouter(ep *Endpoint) *httprouter.Router {
	if ep.Access == Private {
		return s.private
	}
	return s.public
}

func registerRoute(router *httprouter.Router, path string, methods []string, handler httprouter.Handle) {
	for _, method := range expandMethods(methods) {
		router.Handle(method, path, handler)
	}
}

func expandMethods(methods []string) []string {
	var expanded []string
	for _, method := range methods {
		if method == "*" {
			expanded = append(expanded, http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions)
			continue
		}
		expanded = append(expanded, strings.ToUpper(method))
	}
	return expanded
}

func authenticateRequest(req *http.Request, ep *Endpoint) (AuthInfo, error) {
	if ep.Access != Auth {
		return AuthInfo{}, nil
	}
	handler := getAuthHandler()
	if handler == nil {
		return AuthInfo{}, errs.B().Code(errs.Internal).Msg("auth endpoint configured but no auth handler registered").Err()
	}
	params, err := decodeAuthParams(req, handler)
	if err != nil {
		return AuthInfo{}, err
	}
	ctx := context.WithValue(req.Context(), requestStateKey{}, &requestState{
		request: shared.Request{
			Type:     shared.APICall,
			Service:  ep.Service,
			Endpoint: ep.Name,
			Method:   req.Method,
			Path:     req.URL.Path,
			Headers:  req.Header.Clone(),
		},
	})
	info, err := handler.Authenticate(ctx, params)
	if err != nil {
		return AuthInfo{}, err
	}
	if info.UID == "" {
		return AuthInfo{}, errs.B().Code(errs.Unauthenticated).Msg("auth handler returned empty user id").Err()
	}
	return info, nil
}

func decodePathParams(ep *Endpoint, params httprouter.Params) ([]any, shared.PathParams, error) {
	values := make([]any, 0, len(ep.PathParams))
	decoded := make(shared.PathParams, 0, len(ep.PathParams))
	for _, spec := range ep.PathParams {
		raw := strings.TrimPrefix(params.ByName(spec.Name), "/")
		value, err := decodeScalar(spec.Kind, raw)
		if err != nil {
			return nil, nil, errs.B().Code(errs.InvalidArgument).Msgf("invalid path param %q: %v", spec.Name, err).Err()
		}
		values = append(values, value)
		decoded = append(decoded, shared.PathParam{Name: spec.Name, Value: raw})
	}
	return values, decoded, nil
}

func decodeAuthParams(req *http.Request, handler *AuthHandler) (any, error) {
	if handler.ParamType == nil {
		return nil, nil
	}
	if handler.ParamType.Kind() == 0 {
		return nil, nil
	}
	if handler.ParamType.Kind() == reflect.String {
		auth := req.Header.Get("Authorization")
		for _, prefix := range []string{"Bearer ", "Token "} {
			if strings.HasPrefix(auth, prefix) {
				token := strings.TrimSpace(strings.TrimPrefix(auth, prefix))
				if token != "" {
					return token, nil
				}
			}
		}
		return nil, errs.B().Code(errs.Unauthenticated).Msg("invalid auth param").Err()
	}
	return decodeTaggedStruct(req, handler.ParamType, true)
}

func decodeScalar(kind ParamKind, value string) (any, error) {
	return convertScalar(kind, value)
}
