package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"

	"pulse.dev/errs"
	"pulse.dev/runtime/shared"
)

type server struct {
	public  *routeTable
	private *routeTable
	http    *http.Server
}

func newServer(listenAddr string) (*http.Server, error) {
	s := &server{
		public:  newRouteTable(),
		private: newRouteTable(),
	}
	s.public.GlobalOPTIONS = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		applyCORSHeaders(w.Header(), req)
		if allow := w.Header().Get("Allow"); allow != "" {
			w.Header().Set("Access-Control-Allow-Methods", allow)
		}
		if requested := req.Header.Get("Access-Control-Request-Headers"); requested != "" {
			w.Header().Set("Access-Control-Allow-Headers", requested)
		}
		w.WriteHeader(http.StatusNoContent)
	})
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
	s.registerPulseConfig()

	httpServer := &http.Server{
		Addr:    listenAddr,
		Handler: withCORS(s.public),
	}
	s.http = httpServer
	return httpServer, nil
}

type publicConfigResponse struct {
	AppID      string `json:"appID"`
	APIBaseURL string `json:"apiBaseURL"`
}

func (s *server) registerPulseConfig() {
	registerRoute(s.public, "/__pulse/config", []string{http.MethodGet}, func(w http.ResponseWriter, req *http.Request, _ routeParams) {
		meta := Meta()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if err := json.NewEncoder(w).Encode(publicConfigResponse{
			AppID:      meta.AppID,
			APIBaseURL: meta.APIBaseURL,
		}); err != nil {
			errs.HTTPError(w, errs.Wrap(err, "encode pulse config"))
		}
	})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		applyCORSHeaders(w.Header(), req)
		next.ServeHTTP(w, req)
	})
}

func applyCORSHeaders(headers http.Header, req *http.Request) {
	if req == nil {
		return
	}
	origin := strings.TrimSpace(req.Header.Get("Origin"))
	if origin == "" {
		return
	}
	headers.Set("Access-Control-Allow-Origin", origin)
	headers.Set("Access-Control-Allow-Credentials", "true")
	addVary(headers, "Origin", "Authorization")
	if req.Method == http.MethodOptions {
		addVary(headers, "Access-Control-Request-Method", "Access-Control-Request-Headers")
	}
}

func addVary(headers http.Header, values ...string) {
	existing := make(map[string]bool)
	for _, value := range headers.Values("Vary") {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				existing[part] = true
			}
		}
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		existing[value] = true
	}
	if len(existing) == 0 {
		return
	}
	merged := make([]string, 0, len(existing))
	for value := range existing {
		merged = append(merged, value)
	}
	slices.Sort(merged)
	headers.Set("Vary", strings.Join(merged, ", "))
}

func (s *server) registerRaw(ep *Endpoint) {
	handler := func(w http.ResponseWriter, req *http.Request, params routeParams) {
		pathParams := make(shared.PathParams, 0, len(params))
		for _, param := range params {
			pathParams = append(pathParams, shared.PathParam{Name: param.Key, Value: strings.TrimPrefix(param.Value, "/")})
		}

		state := newExternalState(ep, req, pathParams, nil, AuthInfo{})
		ctx := withState(req.Context(), state)
		restore := enterState(state)
		defer restore()
		startRequestTrace(state)

		authInfo, err := authenticateRequest(req.WithContext(ctx), ep)
		if err != nil {
			logRequestStart(state)
			finishRequestTrace(state, errs.HTTPStatus(err), nil, err)
			errs.HTTPError(w, err)
			return
		}
		state.auth = authInfo
		logRequestStart(state)

		status, headers, body, callErr := executeRawEndpoint(ep, req.WithContext(ctx))
		applyHeaders(w.Header(), headers)
		defer finishRequestTrace(state, status, nil, callErr)
		if callErr != nil {
			errs.HTTPErrorWithCode(w, callErr, status)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}

	registerRoute(s.selectRouter(ep), ep.Path, ep.Methods, handler)
}

func (s *server) registerTyped(ep *Endpoint) {
	handler := func(w http.ResponseWriter, req *http.Request, params routeParams) {
		pathValues, pathParams, err := decodePathParams(ep, params)
		if err != nil {
			errs.HTTPError(w, err)
			return
		}

		payload, err := decodePayload(req, ep.PayloadType)
		if err != nil {
			errs.HTTPError(w, err)
			return
		}

		state := newExternalState(ep, req, pathParams, payload, AuthInfo{})
		ctx := withState(req.Context(), state)
		restore := enterState(state)
		defer restore()
		startRequestTrace(state)

		authInfo, err := authenticateRequest(req.WithContext(ctx), ep)
		if err != nil {
			logRequestStart(state)
			finishRequestTrace(state, errs.HTTPStatus(err), nil, err)
			errs.HTTPError(w, err)
			return
		}
		state.auth = authInfo
		logRequestStart(state)

		resp, status, headers, callErr := executeTypedEndpoint(ep, ctx, pathValues, payload)
		applyHeaders(w.Header(), headers)
		defer finishRequestTrace(state, status, resp, callErr)
		if callErr != nil {
			errs.HTTPErrorWithCode(w, callErr, status)
			return
		}
		if err := encodeResponseWithStatus(w, resp, status); err != nil {
			errs.HTTPError(w, errs.Wrap(err, "encode response"))
			return
		}
	}

	registerRoute(s.selectRouter(ep), ep.Path, ep.Methods, handler)
}

func (s *server) selectRouter(ep *Endpoint) *routeTable {
	if ep.Access == Private {
		return s.private
	}
	return s.public
}

func registerRoute(router *routeTable, path string, methods []string, handler routeHandle) {
	router.Handle(methods, path, handler)
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
	ctx := req.Context()
	if stateFromContext(ctx) == nil {
		ctx = context.WithValue(ctx, requestStateKey{}, &requestState{
			request: shared.Request{
				Type:     shared.APICall,
				Service:  ep.Service,
				Endpoint: ep.Name,
				Method:   req.Method,
				Path:     req.URL.Path,
				Headers:  req.Header.Clone(),
			},
		})
	}
	info, err := traceAuthCall(ctx, handler, func(callCtx context.Context) (AuthInfo, error) {
		return handler.Authenticate(callCtx, params)
	})
	if err != nil {
		return AuthInfo{}, err
	}
	if info.UID == "" {
		return AuthInfo{}, errs.B().Code(errs.Unauthenticated).Msg("auth handler returned empty user id").Err()
	}
	return info, nil
}

func decodePathParams(ep *Endpoint, params routeParams) ([]any, shared.PathParams, error) {
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
