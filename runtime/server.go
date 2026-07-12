package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/pprof"
	"slices"
	"strings"
	"sync"
	"time"

	"scenery.sh/errs"
	"scenery.sh/runtime/shared"
)

type server struct {
	public       *routeTable
	private      *routeTable
	contractCORS []contractCORSRoute
	http         *http.Server
	drainOnce    sync.Once
	drainCh      chan struct{}
}

type contractCORSRoute struct {
	path     string
	pathTail bool
	methods  []string
	policy   *ContractHTTPPolicy
}

// beginDrain cancels the contexts of in-flight streaming raw requests so
// their handlers return and net/http writes a proper chunked terminator.
// Without this, http.Server.Shutdown waits out its timeout on SSE/long-poll
// responses and the process exit resets those connections mid-stream.
func (s *server) beginDrain() {
	s.drainOnce.Do(func() {
		close(s.drainCh)
	})
}

func newServer(listenAddr string) (*http.Server, error) {
	s := &server{
		public:  newRouteTable(),
		private: newRouteTable(),
		drainCh: make(chan struct{}),
	}
	s.public.GlobalOPTIONS = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requestedMethod := req.Header.Get("Access-Control-Request-Method")
		if policy := s.contractCORSPolicy(req.URL.EscapedPath(), requestedMethod); policy != nil {
			applyContractCORSHeaders(w.Header(), req, policy)
		} else {
			applyCORSHeaders(w.Header(), req)
		}
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

	endpoints := listEndpoints()
	for _, ep := range endpoints {
		if ep.Raw {
			s.registerRaw(ep)
			continue
		}
		s.registerTyped(ep)
	}
	if storageHTTPConfigured() {
		s.registerStorageRoutes()
	}
	if durableHTTPConfigured() {
		s.registerDurableRoutes()
	}
	if devEndpointsEnabled() {
		s.registerSceneryConfig()
		s.registerPlatformStats()
		s.registerPProf()
	}

	httpServer := &http.Server{
		Addr:    listenAddr,
		Handler: withCORS(withGzip(s.public)),
	}
	for _, endpoint := range endpoints {
		policy := endpoint.ContractPolicy
		if policy == nil {
			continue
		}
		if policy.MaxRequestHeaderBytes > int64(httpServer.MaxHeaderBytes) && policy.MaxRequestHeaderBytes <= int64(^uint(0)>>1) {
			httpServer.MaxHeaderBytes = int(policy.MaxRequestHeaderBytes)
		}
		if timeout := time.Duration(policy.ReadTimeoutNanos); timeout > httpServer.ReadTimeout {
			httpServer.ReadTimeout, httpServer.ReadHeaderTimeout = timeout, timeout
		}
		if timeout := time.Duration(policy.WriteTimeoutNanos); timeout > httpServer.WriteTimeout {
			httpServer.WriteTimeout = timeout
		}
		if timeout := time.Duration(policy.IdleTimeoutNanos); timeout > httpServer.IdleTimeout {
			httpServer.IdleTimeout = timeout
		}
	}
	httpServer.RegisterOnShutdown(s.beginDrain)
	s.http = httpServer
	return httpServer, nil
}

type publicConfigResponse struct {
	AppID        string `json:"appID"`
	BaseAppID    string `json:"baseAppID,omitempty"`
	RuntimeAppID string `json:"runtimeAppID,omitempty"`
	SessionID    string `json:"sessionID,omitempty"`
	APIBaseURL   string `json:"apiBaseURL"`
}

func (s *server) registerSceneryConfig() {
	registerRoute(s.public, "/__scenery/config", []string{http.MethodGet}, func(w http.ResponseWriter, req *http.Request, _ routeParams) {
		meta := Meta()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if err := json.NewEncoder(w).Encode(publicConfigResponse{
			AppID:        meta.AppID,
			BaseAppID:    meta.BaseAppID,
			RuntimeAppID: meta.RuntimeAppID,
			SessionID:    meta.SessionID,
			APIBaseURL:   meta.APIBaseURL,
		}); err != nil {
			errs.HTTPError(w, errs.Wrap(err, "encode scenery config"))
		}
	})
}

func (s *server) registerPlatformStats() {
	handler := func(w http.ResponseWriter, req *http.Request, _ routeParams) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if err := json.NewEncoder(w).Encode(collectPlatformStats()); err != nil {
			errs.HTTPError(w, errs.Wrap(err, "encode platform stats"))
		}
	}
	registerRoute(s.public, "/platform.Stats", []string{http.MethodGet}, handler)
}

func (s *server) registerPProf() {
	routes := []struct {
		path    string
		methods []string
		handler http.HandlerFunc
	}{
		{path: "/debug/pprof/", methods: []string{http.MethodGet}, handler: pprof.Index},
		{path: "/debug/pprof/cmdline", methods: []string{http.MethodGet}, handler: pprof.Cmdline},
		{path: "/debug/pprof/profile", methods: []string{http.MethodGet}, handler: pprof.Profile},
		{path: "/debug/pprof/symbol", methods: []string{http.MethodGet, http.MethodPost}, handler: pprof.Symbol},
		{path: "/debug/pprof/trace", methods: []string{http.MethodGet}, handler: pprof.Trace},
		{path: "/debug/pprof/allocs", methods: []string{http.MethodGet}, handler: pprof.Handler("allocs").ServeHTTP},
		{path: "/debug/pprof/block", methods: []string{http.MethodGet}, handler: pprof.Handler("block").ServeHTTP},
		{path: "/debug/pprof/goroutine", methods: []string{http.MethodGet}, handler: pprof.Handler("goroutine").ServeHTTP},
		{path: "/debug/pprof/heap", methods: []string{http.MethodGet}, handler: pprof.Handler("heap").ServeHTTP},
		{path: "/debug/pprof/mutex", methods: []string{http.MethodGet}, handler: pprof.Handler("mutex").ServeHTTP},
		{path: "/debug/pprof/threadcreate", methods: []string{http.MethodGet}, handler: pprof.Handler("threadcreate").ServeHTTP},
	}
	for _, item := range routes {
		handler := item.handler
		registerRoute(s.public, item.path, item.methods, func(w http.ResponseWriter, req *http.Request, _ routeParams) {
			handler(w, req)
		})
	}
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
	if !corsOriginAllowed(origin) {
		return
	}
	headers.Set("Access-Control-Allow-Origin", origin)
	headers.Set("Access-Control-Allow-Credentials", "true")
	addVary(headers, "Origin", "Authorization")
	if req.Method == http.MethodOptions {
		addVary(headers, "Access-Control-Request-Method", "Access-Control-Request-Headers")
	}
}

func devEndpointsEnabled() bool {
	return envBool("SCENERY_DEV_ENDPOINTS") || envBool("SCENERY_DEV_SUPERVISOR")
}

func corsOriginAllowed(origin string) bool {
	if devEndpointsEnabled() {
		return true
	}
	for item := range strings.SplitSeq(osGetenv("SCENERY_CORS_ALLOW_ORIGINS"), ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if item == "*" || strings.EqualFold(item, origin) {
			return true
		}
	}
	return false
}

func envBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(osGetenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func addVary(headers http.Header, values ...string) {
	existing := make(map[string]bool)
	for _, value := range headers.Values("Vary") {
		for part := range strings.SplitSeq(value, ",") {
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
			if !writeContractAdmissionError(w, ep, err) {
				errs.HTTPError(w, err)
			}
			return
		}
		state.auth = authInfo
		ctx = withRuntimeInvocation(ctx, state)
		logRequestStart(state)

		stream := newRawStreamingResponseWriter(w)
		status := http.StatusOK
		var callErr error
		defer func() { finishRequestTrace(state, status, nil, callErr) }()

		streamCtx, cancelStream := context.WithCancel(ctx)
		defer cancelStream()
		go func() {
			select {
			case <-s.drainCh:
				cancelStream()
			case <-streamCtx.Done():
			}
		}()
		callErr = executeStreamingRawEndpoint(ep, stream, req.WithContext(streamCtx))
		status = stream.StatusCode()
		if callErr != nil && !stream.WroteHeader() {
			status = errs.HTTPStatus(callErr)
			errs.HTTPErrorWithCode(w, callErr, status)
		}
	}

	registerRoute(s.selectRouter(ep), ep.Path, ep.Methods, handler)
}

func (s *server) registerTyped(ep *Endpoint) {
	handler := func(w http.ResponseWriter, req *http.Request, params routeParams) {
		controller := http.NewResponseController(w)
		if ep.ContractPolicy != nil && ep.ContractPolicy.ReadTimeoutNanos > 0 {
			_ = controller.SetReadDeadline(time.Now().Add(time.Duration(ep.ContractPolicy.ReadTimeoutNanos)))
		}
		if ep.ContractPolicy != nil && ep.ContractPolicy.WriteTimeoutNanos > 0 {
			_ = controller.SetWriteDeadline(time.Now().Add(time.Duration(ep.ContractPolicy.WriteTimeoutNanos)))
		}
		applyContractForwardedPolicy(req, ep.ContractPolicy)
		applyContractCORSHeaders(w.Header(), req, ep.ContractPolicy)
		if ep.ContractPolicy != nil && ep.ContractPolicy.MaxRequestHeaderBytes > 0 && contractRequestHeaderBytes(req) > ep.ContractPolicy.MaxRequestHeaderBytes {
			_ = writeContractTransportError(w, &ContractTransportError{Outcome: "transport.invalid_request", Status: http.StatusRequestHeaderFieldsTooLarge, Message: "request headers exceed gateway limit"})
			return
		}
		if ep.ContractPolicy != nil && ep.ContractPolicy.TotalInvocationTimeoutNanos > 0 {
			ctx, cancel := context.WithTimeout(req.Context(), time.Duration(ep.ContractPolicy.TotalInvocationTimeoutNanos))
			defer cancel()
			req = req.WithContext(ctx)
		}
		contractPathValues := make(map[string]string, len(params))
		pathParams := make(shared.PathParams, 0, len(params))
		for _, param := range params {
			value := strings.TrimPrefix(param.Value, "/")
			contractPathValues[param.Key] = value
			pathParams = append(pathParams, shared.PathParam{Name: param.Key, Value: value})
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
		ctx = withRuntimeInvocation(ctx, state)
		decoded, decodeErr := ep.DecodeContractRequest(req.WithContext(ctx), contractPathValues)
		if decodeErr != nil {
			logRequestStart(state)
			decodeStatus := errs.HTTPStatus(decodeErr)
			if transportStatus, ok := contractTransportHTTPStatus(decodeErr); ok {
				decodeStatus = transportStatus
			}
			finishRequestTrace(state, decodeStatus, nil, decodeErr)
			if writeContractTransportError(w, decodeErr) {
				return
			}
			errs.HTTPError(w, decodeErr)
			return
		}
		pathValues, payload := decoded.PathArgs, decoded.Payload
		state.request.Payload = payload
		state.request.PathParams = pathParams
		if authorizationErr := authorizeContractInvocation(ep.ContractPolicy, payload); authorizationErr != nil {
			logRequestStart(state)
			authorizationStatus := errs.HTTPStatus(authorizationErr)
			if admissionStatus, ok := contractAdmissionHTTPStatus(ep, authorizationErr); ok {
				authorizationStatus = admissionStatus
			}
			finishRequestTrace(state, authorizationStatus, nil, authorizationErr)
			if !writeContractAdmissionError(w, ep, authorizationErr) {
				errs.HTTPError(w, authorizationErr)
			}
			return
		}
		logRequestStart(state)

		resp, status, headers, callErr := executeTypedEndpoint(ep, ctx, pathValues, payload)
		if transportStatus, ok := contractTransportHTTPStatus(callErr); ok {
			status = transportStatus
		} else if admissionStatus, ok := contractAdmissionHTTPStatus(ep, callErr); ok {
			status = admissionStatus
		}
		applyHeaders(w.Header(), headers)
		defer func() { finishRequestTrace(state, status, resp, callErr) }()
		if callErr != nil {
			if writeContractTransportError(w, callErr) {
				return
			}
			if writeContractAdmissionError(w, ep, callErr) {
				return
			}
			errs.HTTPErrorWithCode(w, callErr, status)
			return
		}
		encoded, encodeErr := ep.EncodeContractOutcome(req, resp)
		if encodeErr != nil {
			callErr = encodeErr
			status = errs.HTTPStatus(encodeErr)
			if transportStatus, ok := contractTransportHTTPStatus(encodeErr); ok {
				status = transportStatus
			}
			if writeContractTransportError(w, encodeErr) {
				return
			}
			errs.HTTPError(w, errs.Wrap(encodeErr, "encode contract outcome"))
			return
		}
		if encoded.Status != 0 {
			status = encoded.Status
		}
		if status == 0 {
			status = http.StatusOK
		}
		applyHeaders(w.Header(), encoded.Headers)
		w.WriteHeader(status)
		if req.Method != http.MethodHead {
			_, _ = w.Write(encoded.Body)
		}
		return
	}

	if ep.ContractPolicy != nil && ep.Access != Private {
		s.contractCORS = append(s.contractCORS, contractCORSRoute{path: ep.Path, pathTail: ep.ContractPathTail != nil, methods: append([]string(nil), ep.Methods...), policy: ep.ContractPolicy})
	}
	registerEndpointRoute(s.selectRouter(ep), ep, handler)
}

func writeContractAdmissionError(writer http.ResponseWriter, endpoint *Endpoint, err error) bool {
	status, ok := contractAdmissionHTTPStatus(endpoint, err)
	if !ok {
		return false
	}
	outcome := ""
	switch errs.Code(err) {
	case errs.Unauthenticated:
		outcome = "admission.unauthenticated"
	case errs.PermissionDenied:
		outcome = "admission.forbidden"
	case errs.ResourceExhausted:
		outcome = "admission.rate_limited"
	}
	return writeContractTransportError(writer, &ContractTransportError{Outcome: outcome, Status: status, Message: err.Error(), Cause: err})
}

func contractAdmissionHTTPStatus(endpoint *Endpoint, err error) (int, bool) {
	if endpoint == nil || endpoint.ContractPolicy == nil || err == nil {
		return 0, false
	}
	outcome, fallback := "", 0
	switch errs.Code(err) {
	case errs.Unauthenticated:
		outcome, fallback = "admission.unauthenticated", http.StatusUnauthorized
	case errs.PermissionDenied:
		outcome, fallback = "admission.forbidden", http.StatusForbidden
	case errs.ResourceExhausted:
		outcome, fallback = "admission.rate_limited", http.StatusTooManyRequests
	default:
		return 0, false
	}
	if status := endpoint.ContractPolicy.TransportStatuses[outcome]; status != 0 {
		return status, true
	}
	return fallback, true
}

func (s *server) contractCORSPolicy(requestPath, method string) *ContractHTTPPolicy {
	var matches []*route
	policies := map[*route]*ContractHTTPPolicy{}
	for _, corsRoute := range s.contractCORS {
		if method != "" && !routeAllowsMethod(expandMethods(corsRoute.methods), strings.ToUpper(method)) {
			continue
		}
		pattern := parseRoutePattern(corsRoute.path)
		pattern.pathTail = corsRoute.pathTail
		if _, matched := pattern.match(requestPath); matched {
			candidate := &route{pattern: pattern}
			matches = append(matches, candidate)
			policies[candidate] = corsRoute.policy
		}
	}
	slices.SortStableFunc(matches, compareRoutes)
	if len(matches) > 0 {
		return policies[matches[0]]
	}
	return nil
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

func registerEndpointRoute(router *routeTable, endpoint *Endpoint, handler routeHandle) {
	if endpoint.ContractPathTail != nil {
		router.HandlePathTail(endpoint.Methods, endpoint.Path, handler)
		return
	}
	router.Handle(endpoint.Methods, endpoint.Path, handler)
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
	var token string
	for _, prefix := range []string{"Bearer ", "Token "} {
		if after, ok := strings.CutPrefix(req.Header.Get("Authorization"), prefix); ok {
			token = strings.TrimSpace(after)
			break
		}
	}
	if token == "" {
		return AuthInfo{}, errs.B().Code(errs.Unauthenticated).Msg("invalid auth param").Err()
	}
	ctx := req.Context()
	if stateFromContext(ctx) == nil {
		request := shared.Request{
			Type:     shared.APICall,
			Service:  ep.Service,
			Endpoint: ep.Name,
			Method:   req.Method,
			Path:     req.URL.Path,
			Headers:  req.Header.Clone(),
		}
		ctx = context.WithValue(ctx, requestStateKey{}, &requestState{
			request:      request,
			logsEnabled:  logsEnabledForRequest(request),
			traceEnabled: traceEnabledForRequest(request),
		})
	}
	info, err := traceAuthCall(ctx, handler, func(callCtx context.Context) (AuthInfo, error) {
		return handler.Authenticate(callCtx, token)
	})
	if err != nil {
		return AuthInfo{}, err
	}
	if info.UID == "" {
		return AuthInfo{}, errs.B().Code(errs.Unauthenticated).Msg("auth handler returned empty user id").Err()
	}
	return info, nil
}
