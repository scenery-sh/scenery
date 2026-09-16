package runtime

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"scenery.sh/errs"
)

// A process host is the stable front of a development session whose services
// run as separate processes. It links no service implementation. The supervisor
// publishes numbered application generations naming each service process
// instance, its private socket and its linked identity. The host pins every
// request to the generation current at ingress, forwards it unmodified to the
// owning instance of that generation, dispatches internal calls of pinned
// requests within the same generation, and rejects answers whose identity does
// not match the published instance. A generation is retired when no request
// pinned to it is in flight, or when the supervisor forces its retirement.

const (
	processGenerationsPath       = "/__scenery/process/v1/generations"
	processHostMaxHeaderBytes    = 64 << 20
	processHostManifestMaxBytes  = 4 << 20
	processHostShutdownGrace     = 5 * time.Second
	processHostUnavailableReason = "service process %s is unavailable"
)

// ProcessHostRoute names the service process that registers one HTTP endpoint.
type ProcessHostRoute struct {
	Process  string
	Methods  []string
	Path     string
	PathTail bool
}

// ProcessHostMCPTool names the service process that registers one MCP tool.
type ProcessHostMCPTool struct {
	Process          string
	AssistantAddress string
	Name             string
}

// ProcessHostConfig is rendered into the generated host entrypoint. Fallback
// serves framework routes and requests that match no contract route; an
// application without a native service has none, and its host serves them.
type ProcessHostConfig struct {
	Name       string
	ListenAddr string
	Routes     []ProcessHostRoute
	MCPTools   []ProcessHostMCPTool
	Fallback   string
}

type processGenerationManifest struct {
	Generation       uint64                               `json:"generation"`
	ContractRevision string                               `json:"contract_revision"`
	Processes        map[string]processGenerationInstance `json:"processes"`
	Bindings         map[string]string                    `json:"bindings"`
}

type processGenerationInstance struct {
	Network  string                  `json:"network"`
	Address  string                  `json:"address"`
	PID      int                     `json:"pid"`
	Identity processInstanceIdentity `json:"identity"`
}

type processInstanceIdentity struct {
	ContractRevision       string `json:"contract_revision"`
	ImplementationRevision string `json:"implementation_revision"`
	BuildInputDigest       string `json:"build_input_digest"`
	GoTarget               string `json:"go_target"`
}

type processGenerationStatus struct {
	Current     uint64                         `json:"current"`
	Generations []processGenerationStatusEntry `json:"generations"`
}

type processGenerationStatusEntry struct {
	Generation uint64                             `json:"generation"`
	InFlight   int64                              `json:"in_flight"`
	Processes  map[string]int                     `json:"processes"`
	Instances  map[string]processInstanceIdentity `json:"instances"`
}

type processHost struct {
	name     string
	token    string
	contract string
	routes   *routeTable
	fallback string
	required []string
	mcpTools map[string]string

	// local serves the host's own application-level endpoints (assistant
	// gateways) that localRoutes matches.
	local       http.Handler
	localRoutes *routeTable

	owners processHostDurableOwners
	faults processHostFaults

	mu          sync.RWMutex
	current     *processHostGeneration
	generations map[uint64]*processHostGeneration
}

type processHostGeneration struct {
	number    uint64
	instances map[string]*processHostInstance
	bindings  map[string]string
	inFlight  atomic.Int64
}

type processHostInstance struct {
	name     string
	spec     processGenerationInstance
	ingress  http.Handler
	dispatch http.Handler
	client   *http.Client
}

type processHostGenerationKey struct{}

// MainProcessHost serves a process host until the supervisor or a signal stops
// it: public requests on the runtime listen address, and dispatch plus generation
// control on the private dispatch listener of the process link.
func MainProcessHost(cfg ProcessHostConfig) error {
	link, err := currentProcessLink()
	if err != nil {
		return err
	}
	if link == nil {
		return fmt.Errorf("runtime: process host requires SCENERY_PROCESS_LINK")
	}
	host, err := newProcessHost(cfg, link.Token, CurrentLinkedContractBundle().ContractRevision)
	if err != nil {
		return err
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ListenAddrFromEnv()
	}
	SetAppConfig(AppConfig{Name: cfg.Name, ListenAddr: cfg.ListenAddr})
	stopReporting := startDevelopmentReporting(AppConfig{Name: cfg.Name, ListenAddr: cfg.ListenAddr})
	defer stopReporting()
	// Application-level registrations (assistant gateways and their private MCP
	// gateways) run in the host and reach service-owned MCP tools through it.
	setActiveProcessHost(host)
	defer setActiveProcessHost(nil)
	if err := InitializeServices(); err != nil {
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), processHostShutdownGrace)
		defer cancelShutdown()
		return errors.Join(err, ShutdownServices(shutdownCtx))
	}
	defer func() {
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), processHostShutdownGrace)
		defer cancelShutdown()
		_ = ShutdownServices(shutdownCtx)
	}()
	if endpoints := listEndpoints(); len(endpoints) > 0 || cfg.Fallback == "" {
		local, err := newServer(cfg.ListenAddr)
		if err != nil {
			return err
		}
		host.local, host.localRoutes = local.Handler, newRouteTable()
		for _, endpoint := range endpoints {
			registerEndpointRoute(host.localRoutes, endpoint, func(http.ResponseWriter, *http.Request, routeParams) {})
		}
	}
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	stopSupervisorMonitor := startSupervisorParentMonitor(cancelRun)
	defer stopSupervisorMonitor()
	sigCtx, stopSignals := signal.NotifyContext(runCtx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	control, err := listenRuntime(link.Dispatch.Network, link.Dispatch.Address)
	if err != nil {
		return err
	}
	public, err := listenRuntime(ListenNetworkFromEnv(), cfg.ListenAddr)
	if err != nil {
		_ = control.Close()
		return err
	}
	servers := []*http.Server{
		{Handler: http.HandlerFunc(host.serveControl), MaxHeaderBytes: processHostMaxHeaderBytes},
		{Handler: http.HandlerFunc(host.serveIngress), MaxHeaderBytes: processHostMaxHeaderBytes},
	}
	errCh := make(chan error, 2)
	for index, listener := range []net.Listener{control, public} {
		go func() { errCh <- servers[index].Serve(listener) }()
	}
	logTrace(context.Background(), fmt.Sprintf("process host %s forwarding %d routes", cfg.Name, len(cfg.Routes)))
	var serveErr error
	select {
	case <-sigCtx.Done():
	case serveErr = <-errCh:
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), processHostShutdownGrace)
	defer cancel()
	shutdownErrs := []error{}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		shutdownErrs = append(shutdownErrs, serveErr)
	}
	for _, server := range servers {
		if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			shutdownErrs = append(shutdownErrs, err)
		}
	}
	return errors.Join(shutdownErrs...)
}

func newProcessHost(cfg ProcessHostConfig, token, contract string) (*processHost, error) {
	cfg.Fallback = strings.TrimSpace(cfg.Fallback)
	if cfg.Fallback == "" && (len(cfg.Routes) > 0 || len(cfg.MCPTools) > 0) {
		return nil, fmt.Errorf("runtime: process host with service routes requires a fallback process")
	}
	host := &processHost{name: cfg.Name, token: token, contract: contract, routes: newRouteTable(), fallback: cfg.Fallback, generations: map[uint64]*processHostGeneration{}, mcpTools: map[string]string{}}
	required := map[string]bool{}
	if cfg.Fallback != "" {
		required[cfg.Fallback] = true
	}
	for _, tool := range cfg.MCPTools {
		key := tool.AssistantAddress + "\x00" + tool.Name
		if strings.TrimSpace(tool.Process) == "" || strings.TrimSpace(tool.Name) == "" || host.mcpTools[key] != "" {
			return nil, fmt.Errorf("runtime: process host MCP tool %q for %q is invalid", tool.Name, tool.Process)
		}
		host.mcpTools[key] = tool.Process
		required[tool.Process] = true
	}
	for _, route := range cfg.Routes {
		if strings.TrimSpace(route.Process) == "" || len(route.Methods) == 0 || !strings.HasPrefix(route.Path, "/") {
			return nil, fmt.Errorf("runtime: process host route %q for %q is invalid", route.Path, route.Process)
		}
		required[route.Process] = true
		process := route.Process
		handle := func(w http.ResponseWriter, req *http.Request, _ routeParams) { host.forward(w, req, process) }
		if route.PathTail {
			host.routes.HandlePathTail(route.Methods, route.Path, handle)
		} else {
			host.routes.Handle(route.Methods, route.Path, handle)
		}
	}
	for process := range required {
		host.required = append(host.required, process)
	}
	sort.Strings(host.required)
	return host, nil
}

func (h *processHost) serveIngress(w http.ResponseWriter, req *http.Request) {
	if h.local != nil {
		method := req.Method
		if requested := strings.TrimSpace(req.Header.Get("Access-Control-Request-Method")); method == http.MethodOptions && requested != "" {
			method = requested
		}
		if h.localRoutes.ownerRoute(req.URL.EscapedPath(), method) != nil {
			if current := h.currentGeneration(); current != 0 {
				w.Header().Set(processGenerationHeader, strconv.FormatUint(current, 10))
			}
			h.local.ServeHTTP(w, req)
			return
		}
	}
	generation := h.acquire(0)
	if generation == nil {
		errs.HTTPErrorWithCode(w, errs.B().Code(errs.Unavailable).Msg("application generation is not published").Err(), http.StatusServiceUnavailable)
		return
	}
	defer generation.inFlight.Add(-1)
	// An answer names the application generation that served it, so a check can
	// bind its evidence to the exact published implementation set.
	w.Header().Set(processGenerationHeader, strconv.FormatUint(generation.number, 10))
	req.Header.Set(processGenerationHeader, strconv.FormatUint(generation.number, 10))
	req = req.WithContext(context.WithValue(req.Context(), processHostGenerationKey{}, generation))
	method := req.Method
	if requested := strings.TrimSpace(req.Header.Get("Access-Control-Request-Method")); method == http.MethodOptions && requested != "" {
		method = requested
	}
	if owner := h.routes.ownerRoute(req.URL.EscapedPath(), method); owner != nil {
		owner.handler(w, req, nil)
		return
	}
	if h.fallback == "" {
		// An application without a native service: the host's own runtime
		// serves framework routes and answers unmatched requests.
		h.local.ServeHTTP(w, req)
		return
	}
	h.forward(w, req, h.fallback)
}

func (h *processHost) forward(w http.ResponseWriter, req *http.Request, process string) {
	generation, _ := req.Context().Value(processHostGenerationKey{}).(*processHostGeneration)
	instance := generation.instances[process]
	if instance == nil {
		errs.HTTPErrorWithCode(w, errs.B().Code(errs.Unavailable).Msgf(processHostUnavailableReason, process).Err(), http.StatusServiceUnavailable)
		return
	}
	if h.applyIngressFault(w, req, process) {
		return
	}
	instance.ingress.ServeHTTP(w, req)
}

func (h *processHost) serveControl(w http.ResponseWriter, req *http.Request) {
	token, found := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
	if !found || subtle.ConstantTimeCompare([]byte(token), []byte(h.token)) != 1 {
		writeProcessLinkResponse(w, http.StatusUnauthorized, processLinkResponse{Error: &processLinkError{Kind: "error", Message: "permission_denied: process link token rejected"}})
		return
	}
	switch {
	case req.URL.Path == processLinkBindingPath && req.Method == http.MethodPost:
		h.dispatch(w, req)
	case req.URL.Path == processGenerationsPath && req.Method == http.MethodPut:
		h.servePublish(w, req)
	case req.URL.Path == processGenerationsPath && req.Method == http.MethodGet:
		writeProcessHostJSON(w, http.StatusOK, h.status())
	case req.URL.Path == processFaultsPath && (req.Method == http.MethodPut || req.Method == http.MethodGet):
		h.serveFaults(w, req)
	case strings.HasPrefix(req.URL.Path, processGenerationsPath+"/") && req.Method == http.MethodDelete:
		number, err := strconv.ParseUint(strings.TrimPrefix(req.URL.Path, processGenerationsPath+"/"), 10, 64)
		force := req.URL.Query().Get("force")
		if err != nil || number == 0 || force != "" && force != "true" {
			http.Error(w, "invalid generation retirement", http.StatusBadRequest)
			return
		}
		status, message := h.retire(number, force == "true")
		if status == http.StatusNoContent {
			w.WriteHeader(status)
			return
		}
		http.Error(w, message, status)
	default:
		http.NotFound(w, req)
	}
}

// dispatch sends one internal call to the owning instance of the generation the
// caller's request is pinned to, or of the current generation when unpinned.
func (h *processHost) dispatch(w http.ResponseWriter, req *http.Request) {
	address := req.Header.Get(processLinkBindingHeader)
	var number uint64
	if value := req.Header.Get(processGenerationHeader); value != "" {
		parsed, err := strconv.ParseUint(value, 10, 64)
		if err != nil || parsed == 0 {
			writeProcessLinkResponse(w, http.StatusBadRequest, processLinkResponse{Error: &processLinkError{Kind: "error", Message: "invalid_argument: invalid process generation"}})
			return
		}
		number = parsed
	}
	req.Header.Del(processGenerationHeader)
	generation := h.acquire(number)
	if generation == nil {
		writeProcessLinkUnavailable(w, fmt.Sprintf("application generation %d is not dispatchable", number), "not_sent")
		return
	}
	defer generation.inFlight.Add(-1)
	process := generation.bindings[address]
	instance := generation.instances[process]
	if instance == nil {
		writeProcessLinkResponse(w, http.StatusNotFound, processLinkResponse{Error: &processLinkError{Kind: "error", Message: fmt.Sprintf("contract internal binding %s is not registered", address)}})
		return
	}
	if h.applyProcessFault(w, req, process, address) {
		return
	}
	instance.dispatch.ServeHTTP(w, req)
}

// currentGeneration reports the published generation without pinning work to it.
func (h *processHost) currentGeneration() uint64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.current == nil {
		return 0
	}
	return h.current.number
}

func (h *processHost) acquire(number uint64) *processHostGeneration {
	h.mu.RLock()
	defer h.mu.RUnlock()
	generation := h.current
	if number != 0 {
		generation = h.generations[number]
	}
	if generation != nil {
		generation.inFlight.Add(1)
	}
	return generation
}

func (h *processHost) servePublish(w http.ResponseWriter, req *http.Request) {
	var manifest processGenerationManifest
	decoder := json.NewDecoder(io.LimitReader(req.Body, processHostManifestMaxBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		http.Error(w, "malformed generation manifest", http.StatusBadRequest)
		return
	}
	if err := h.publish(manifest); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *processHost) publish(manifest processGenerationManifest) error {
	if manifest.ContractRevision != h.contract {
		return fmt.Errorf("generation %d contract revision %q differs from the host's %q", manifest.Generation, manifest.ContractRevision, h.contract)
	}
	for _, process := range h.required {
		if _, ok := manifest.Processes[process]; !ok {
			return fmt.Errorf("generation %d has no instance of service process %s", manifest.Generation, process)
		}
	}
	for address, process := range manifest.Bindings {
		if _, ok := manifest.Processes[process]; !ok {
			return fmt.Errorf("generation %d binding %s names unknown process %s", manifest.Generation, address, process)
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.current != nil && manifest.Generation <= h.current.number || manifest.Generation == 0 {
		return fmt.Errorf("generation %d does not follow the published generation", manifest.Generation)
	}
	generation := &processHostGeneration{number: manifest.Generation, instances: map[string]*processHostInstance{}, bindings: maps.Clone(manifest.Bindings)}
	for name, spec := range manifest.Processes {
		identity := spec.Identity
		if !validProcessLinkTarget(processLinkTarget{Network: spec.Network, Address: spec.Address}) || spec.PID <= 0 || identity.ContractRevision != manifest.ContractRevision ||
			identity.ImplementationRevision == "" || identity.BuildInputDigest == "" || identity.GoTarget == "" {
			return fmt.Errorf("generation %d instance of %s is invalid", manifest.Generation, name)
		}
		if h.current != nil {
			if previous := h.current.instances[name]; previous != nil && previous.spec == spec {
				generation.instances[name] = previous
				continue
			}
		}
		generation.instances[name] = newProcessHostInstance(name, spec)
	}
	h.generations[generation.number] = generation
	h.current = generation
	return nil
}

// retire removes a replaced generation when no work is pinned to it. A forced
// retirement removes it regardless: its pinned internal calls then fail as not
// sent, and work already forwarded ends when the supervisor stops its instances.
func (h *processHost) retire(number uint64, force bool) (int, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	generation := h.generations[number]
	switch {
	case generation == nil:
		return http.StatusNotFound, "generation is not published"
	case generation == h.current:
		return http.StatusConflict, "the current generation cannot be retired"
	case !force && generation.inFlight.Load() > 0:
		return http.StatusConflict, fmt.Sprintf("generation has %d requests in flight", generation.inFlight.Load())
	}
	delete(h.generations, number)
	return http.StatusNoContent, ""
}

func (h *processHost) status() processGenerationStatus {
	h.mu.RLock()
	defer h.mu.RUnlock()
	status := processGenerationStatus{Generations: []processGenerationStatusEntry{}}
	if h.current != nil {
		status.Current = h.current.number
	}
	for number, generation := range h.generations {
		entry := processGenerationStatusEntry{Generation: number, InFlight: generation.inFlight.Load(), Processes: map[string]int{}, Instances: map[string]processInstanceIdentity{}}
		for name, instance := range generation.instances {
			entry.Processes[name] = instance.spec.PID
			entry.Instances[name] = instance.spec.Identity
		}
		status.Generations = append(status.Generations, entry)
	}
	sort.Slice(status.Generations, func(i, j int) bool { return status.Generations[i].Generation < status.Generations[j].Generation })
	return status
}

var processHostForwardedHeaders = [...]string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"}

func newProcessHostInstance(name string, spec processGenerationInstance) *processHostInstance {
	dialer := &net.Dialer{}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			conn, err := dialer.DialContext(ctx, spec.Network, spec.Address)
			if err != nil {
				return nil, &processLinkDialError{err: err}
			}
			return conn, nil
		},
		DisableCompression:  true,
		MaxIdleConnsPerHost: 64,
		IdleConnTimeout:     90 * time.Second,
	}
	verify := func(response *http.Response) error {
		if !processIdentityMatches(response.Header, spec) {
			return &processIdentityMismatchError{process: name}
		}
		return nil
	}
	rewrite := func(request *httputil.ProxyRequest) {
		request.Out.URL.Scheme, request.Out.URL.Host = "http", "scenery-process"
		// The reverse proxy drops query parameters it cannot parse before
		// Rewrite. The owning process decodes the raw query under the contract's
		// own rules, so it must receive exactly the query the host received.
		request.Out.URL.RawQuery = request.In.URL.RawQuery
		request.Out.Host = request.In.Host
		// The owning process applies the gateway's forwarded-header policy to
		// the headers the host received, not to a host-rewritten set.
		for _, header := range processHostForwardedHeaders {
			if values := request.In.Header.Values(header); len(values) > 0 {
				request.Out.Header[header] = append([]string(nil), values...)
			}
		}
	}
	return &processHostInstance{
		name: name, spec: spec, client: &http.Client{Transport: transport},
		ingress: &httputil.ReverseProxy{Rewrite: rewrite, Transport: transport, FlushInterval: -1, ModifyResponse: verify,
			ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
				logTrace(req.Context(), fmt.Sprintf("service process %s did not answer: %v", name, err))
				errs.HTTPErrorWithCode(w, errs.B().Code(errs.Unavailable).Msgf(processHostUnavailableReason, name).Err(), http.StatusServiceUnavailable)
			}},
		dispatch: &httputil.ReverseProxy{Rewrite: rewrite, Transport: transport, FlushInterval: -1, ModifyResponse: verify,
			ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
				delivery := "unknown"
				if _, notSent := errors.AsType[*processLinkDialError](err); notSent {
					delivery = "not_sent"
				}
				writeProcessLinkUnavailable(w, fmt.Sprintf(processHostUnavailableReason, name), delivery)
			}},
	}
}

func processIdentityMatches(headers http.Header, spec processGenerationInstance) bool {
	return headers.Get(processIdentityContractHdr) == spec.Identity.ContractRevision &&
		headers.Get(processIdentityImplHeader) == spec.Identity.ImplementationRevision &&
		headers.Get(processIdentityBuildHeader) == spec.Identity.BuildInputDigest &&
		headers.Get(processIdentityTargetHeader) == spec.Identity.GoTarget &&
		headers.Get(processIdentityPIDHeader) == strconv.Itoa(spec.PID)
}

type processIdentityMismatchError struct{ process string }

func (e *processIdentityMismatchError) Error() string {
	return fmt.Sprintf("service process %s answered with an identity other than its published instance", e.process)
}

func writeProcessLinkUnavailable(w http.ResponseWriter, message, delivery string) {
	writeProcessLinkResponse(w, http.StatusOK, processLinkResponse{Error: &processLinkError{Kind: "errs", Code: errs.Unavailable, Message: message, Meta: errs.Metadata{"delivery": delivery}}})
}

func writeProcessHostJSON(w http.ResponseWriter, status int, value any) {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(value); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body.Bytes())
}
