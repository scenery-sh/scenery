package runtime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"scenery.sh/errs"
)

// A process host is the stable front of a development session whose services
// run as separate processes. It links no service implementation: it selects the
// process that owns each request with the same route table the runtime server
// uses and forwards the unmodified request, so decoding, policies, outcome
// encoding, streaming and response identity stay in the owning process.

// ProcessHostRoute names the service process that registers one HTTP endpoint.
type ProcessHostRoute struct {
	Process  string
	Methods  []string
	Path     string
	PathTail bool
}

// ProcessHostConfig is rendered into the generated host entrypoint. Fallback
// serves framework routes and requests that match no contract route.
type ProcessHostConfig struct {
	Name       string
	ListenAddr string
	Routes     []ProcessHostRoute
	Fallback   string
}

// processHostMaxHeaderBytes leaves request header limits to the owning process,
// which applies the endpoint's declared policy.
const processHostMaxHeaderBytes = 64 << 20

// MainProcessHost serves a process host until the supervisor or a signal stops it.
func MainProcessHost(cfg ProcessHostConfig) error {
	link, err := currentProcessLink()
	if err != nil {
		return err
	}
	if link == nil {
		return fmt.Errorf("runtime: process host requires SCENERY_PROCESS_LINK")
	}
	handler, err := newProcessHostHandler(cfg, link)
	if err != nil {
		return err
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ListenAddrFromEnv()
	}
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	stopSupervisorMonitor := startSupervisorParentMonitor(cancelRun)
	defer stopSupervisorMonitor()
	sigCtx, stopSignals := signal.NotifyContext(runCtx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	listener, err := listenRuntime(ListenNetworkFromEnv(), cfg.ListenAddr)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: handler, MaxHeaderBytes: processHostMaxHeaderBytes}
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(listener) }()
	logTrace(context.Background(), fmt.Sprintf("process host %s forwarding %d routes", cfg.Name, len(cfg.Routes)))
	select {
	case <-sigCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

type processHostHandler struct {
	routes   *routeTable
	fallback http.Handler
}

func newProcessHostHandler(cfg ProcessHostConfig, link *processLinkConfig) (*processHostHandler, error) {
	proxies := map[string]http.Handler{}
	proxy := func(process string) (http.Handler, error) {
		if existing := proxies[process]; existing != nil {
			return existing, nil
		}
		target, ok := link.Processes[process]
		if strings.TrimSpace(process) == "" || !ok {
			return nil, fmt.Errorf("runtime: process link has no target for service process %q", process)
		}
		forward := newProcessHostProxy(process, target)
		proxies[process] = forward
		return forward, nil
	}
	fallback, err := proxy(cfg.Fallback)
	if err != nil {
		return nil, err
	}
	handler := &processHostHandler{routes: newRouteTable(), fallback: fallback}
	for _, route := range cfg.Routes {
		forward, err := proxy(route.Process)
		if err != nil {
			return nil, err
		}
		if len(route.Methods) == 0 || !strings.HasPrefix(route.Path, "/") {
			return nil, fmt.Errorf("runtime: process host route %q for %s is invalid", route.Path, route.Process)
		}
		handle := func(w http.ResponseWriter, req *http.Request, _ routeParams) { forward.ServeHTTP(w, req) }
		if route.PathTail {
			handler.routes.HandlePathTail(route.Methods, route.Path, handle)
		} else {
			handler.routes.Handle(route.Methods, route.Path, handle)
		}
	}
	return handler, nil
}

func (h *processHostHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	method := req.Method
	if requested := strings.TrimSpace(req.Header.Get("Access-Control-Request-Method")); method == http.MethodOptions && requested != "" {
		method = requested
	}
	if owner := h.routes.ownerRoute(req.URL.EscapedPath(), method); owner != nil {
		owner.handler(w, req, nil)
		return
	}
	h.fallback.ServeHTTP(w, req)
}

var processHostForwardedHeaders = [...]string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"}

func newProcessHostProxy(process string, target processLinkTarget) http.Handler {
	dialer := &net.Dialer{}
	return &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			request.Out.URL.Scheme, request.Out.URL.Host = "http", "scenery-process"
			request.Out.Host = request.In.Host
			// The owning process applies the gateway's forwarded-header policy
			// to the headers the host received, not to a host-rewritten set.
			for _, name := range processHostForwardedHeaders {
				if values := request.In.Header.Values(name); len(values) > 0 {
					request.Out.Header[name] = append([]string(nil), values...)
				}
			}
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, target.Network, target.Address)
			},
			DisableCompression:  true,
			MaxIdleConnsPerHost: 64,
			IdleConnTimeout:     90 * time.Second,
		},
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
			logTrace(req.Context(), fmt.Sprintf("service process %s is unavailable: %v", process, err))
			errs.HTTPErrorWithCode(w, errs.B().Code(errs.Unavailable).Msgf("service process %s is unavailable", process).Err(), http.StatusServiceUnavailable)
		},
	}
}
