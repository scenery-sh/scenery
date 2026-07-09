package agent

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"scenery.sh/internal/localproxy"
)

type RunOptions struct {
	// Home overrides the agent home directory; when empty, the home is
	// resolved from the environment (see DefaultPaths).
	Home             string
	SocketPath       string
	RouterAddr       string
	RouterTLS        bool
	InstallTrust     bool
	DashboardBackend Backend
	Identity         Identity
	JSON             bool
}

type Server struct {
	paths                Paths
	registry             *Registry
	routerAddr           string
	publicRouterAddr     string
	routerScheme         string
	internalRouterScheme string
	edge                 *EdgeState
	edgeToken            string
	dashboard            Backend
	identity             Identity
	tlsCA                localproxy.LocalCA
	tlsCerts             sync.Map
	control              *http.Server
	router               *http.Server
	controlLn            net.Listener
	routerLn             net.Listener
}

func NewServer(opts RunOptions) (*Server, error) {
	var paths Paths
	if opts.Home != "" {
		paths = PathsForHome(opts.Home)
	} else {
		var err error
		paths, err = DefaultPaths()
		if err != nil {
			return nil, err
		}
	}
	if opts.SocketPath != "" {
		paths.SocketPath = filepath.Clean(opts.SocketPath)
		paths.RunDir = filepath.Dir(paths.SocketPath)
	}
	if err := EnsureDirs(paths); err != nil {
		return nil, err
	}
	routerAddr := strings.TrimSpace(opts.RouterAddr)
	if routerAddr == "" {
		routerAddr = RouterAddrFromEnv()
	}
	routerTLS := opts.RouterTLS
	routerScheme := "http"
	if routerTLS {
		routerScheme = "https"
	}
	installTrust := opts.InstallTrust || TrustFromEnv()
	routerLn, actualRouterAddr, err := listenRouter(routerAddr)
	if err != nil {
		return nil, err
	}
	var tlsCA localproxy.LocalCA
	if routerTLS {
		tlsCA, err = localproxy.LoadOrCreateLocalCA()
		if err != nil {
			_ = routerLn.Close()
			return nil, err
		}
		if installTrust {
			trusted, trustErr := localproxy.LocalCATrusted(tlsCA.CertPath)
			if trustErr != nil {
				slog.Warn("failed to check scenery local CA trust", "err", trustErr)
			}
			if !trusted {
				if err := localproxy.InstallLocalCATrust(tlsCA.CertPath); err != nil {
					slog.Warn("failed to install scenery local CA trust", "err", err)
				}
			}
		}
	}
	edgeState, edgeErr := LoadEdgeState(paths.EdgeStatePath)
	if edgeErr != nil {
		slog.Warn("failed to read scenery edge state", "err", edgeErr)
	}
	publicRouterAddr := actualRouterAddr
	routeScheme := routerScheme
	var activeEdge *EdgeState
	if EdgeStateRunning(edgeState) && edgeState.PublicAddr != "" {
		edgeCopy := edgeState
		activeEdge = &edgeCopy
		publicRouterAddr = edgeState.PublicAddr
		if edgeState.PublicScheme != "" {
			routeScheme = edgeState.PublicScheme
		}
	}
	registry, err := OpenRegistry(paths.RegistryPath, publicRouterAddr, routeScheme)
	if err != nil {
		_ = routerLn.Close()
		return nil, err
	}
	controlLn, err := listenUnixSocket(paths.SocketPath)
	if err != nil {
		_ = routerLn.Close()
		return nil, err
	}
	server := &Server{
		paths:                paths,
		registry:             registry,
		routerAddr:           actualRouterAddr,
		publicRouterAddr:     publicRouterAddr,
		routerScheme:         routeScheme,
		internalRouterScheme: routerScheme,
		edge:                 activeEdge,
		edgeToken:            readEdgeToken(paths.EdgeTokenPath),
		dashboard:            normalizeBackend(opts.DashboardBackend),
		identity:             opts.Identity,
		tlsCA:                tlsCA,
		controlLn:            controlLn,
		routerLn:             routerLn,
	}
	server.control = &http.Server{Handler: server.controlMux()}
	server.router = &http.Server{Handler: server.routerMux()}
	return server, nil
}

func (s *Server) Run(ctx context.Context) error {
	if err := s.writeState(); err != nil {
		_ = s.Close()
		return err
	}
	errCh := make(chan error, 2)
	go func() {
		if err := s.control.Serve(s.controlLn); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			errCh <- fmt.Errorf("control server: %w", err)
			return
		}
		errCh <- nil
	}()
	go func() {
		ln := s.routerLn
		if s.internalRouterScheme == "https" {
			ln = tls.NewListener(s.routerLn, s.routerTLSConfig())
		}
		if err := s.router.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			errCh <- fmt.Errorf("router server: %w", err)
			return
		}
		errCh <- nil
	}()
	select {
	case <-ctx.Done():
		return s.Close()
	case err := <-errCh:
		closeErr := s.Close()
		return errors.Join(err, closeErr)
	}
}

func (s *Server) Paths() Paths {
	if s == nil {
		return Paths{}
	}
	return s.paths
}

func (s *Server) RouterAddr() string {
	if s == nil {
		return ""
	}
	return s.routerAddr
}

func (s *Server) RouterScheme() string {
	if s == nil || s.routerScheme == "" {
		return "http"
	}
	return s.routerScheme
}

func (s *Server) PublicRouterAddr() string {
	if s == nil || strings.TrimSpace(s.publicRouterAddr) == "" {
		return ""
	}
	return s.publicRouterAddr
}

func (s *Server) ListSessions() []Session {
	if s == nil || s.registry == nil {
		return nil
	}
	return s.registry.List()
}

func (s *Server) GetSession(id string) (Session, bool) {
	if s == nil || s.registry == nil {
		return Session{}, false
	}
	return s.registry.Get(id)
}

func (s *Server) GetSubstrate(kind string) (Substrate, bool) {
	if s == nil || s.registry == nil {
		return Substrate{}, false
	}
	return s.registry.GetSubstrate(kind)
}

func (s *Server) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var errs []error
	if s.control != nil {
		if err := s.control.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs = append(errs, err)
		}
	}
	if s.router != nil {
		if err := s.router.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs = append(errs, err)
		}
	}
	if s.controlLn != nil {
		if err := s.controlLn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			errs = append(errs, err)
		}
	}
	if s.routerLn != nil {
		if err := s.routerLn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			errs = append(errs, err)
		}
	}
	if s.registry != nil {
		for _, substrate := range s.registry.ListSubstrates() {
			substrateOwnerVerified := false
			if substrate.OwnerPID > 0 || substrate.Owner.PID > 0 {
				if _, err := ownerForSignal(substrate.OwnerPID, substrate.Owner); err == nil {
					substrateOwnerVerified = true
				} else {
					slog.Warn("substrate owner fingerprint did not verify; component owners are required for safe interrupt", "kind", substrate.Kind, "pid", firstPositive(substrate.Owner.PID, substrate.OwnerPID), "err", err)
				}
			}
			for name, pid := range substrate.PIDs {
				if pid <= 0 {
					continue
				}
				if owner := substrate.Owners[name]; owner.PID > 0 {
					if owner.PID != pid {
						slog.Warn("skipping substrate component interrupt because owner pid does not match component pid", "kind", substrate.Kind, "component", name, "pid", pid, "owner_pid", owner.PID)
						continue
					}
					if _, err := ownerForSignal(pid, owner); err != nil {
						slog.Warn("skipping substrate component interrupt because owner fingerprint did not verify", "kind", substrate.Kind, "component", name, "pid", pid, "err", err)
						continue
					}
				} else if !substrateOwnerVerified {
					slog.Warn("skipping substrate component interrupt because no verified owner fingerprint is available", "kind", substrate.Kind, "component", name, "pid", pid)
					continue
				}
				if err := interruptProcess(pid); err != nil {
					slog.Warn("failed to interrupt scenery substrate process", "kind", substrate.Kind, "component", name, "pid", pid, "err", err)
				}
			}
		}
	}
	if s.paths.SocketPath != "" {
		if err := os.Remove(s.paths.SocketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func normalizeBackend(backend Backend) Backend {
	backend.Network = strings.TrimSpace(backend.Network)
	if backend.Network == "" {
		backend.Network = "tcp"
	}
	backend.Addr = strings.TrimSpace(backend.Addr)
	if backend.Addr == "" {
		return Backend{}
	}
	return backend
}

func (s *Server) routerTLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"h2", "http/1.1"},
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			host := ""
			if hello != nil {
				host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(hello.ServerName)), ".")
			}
			if host == "" {
				host = "localhost"
			}
			if !s.agentTLSHostAllowed(host) {
				return nil, fmt.Errorf("unsupported scenery agent TLS host %q", host)
			}
			if cert, ok := s.tlsCerts.Load(host); ok {
				tlsCert := cert.(tls.Certificate)
				return &tlsCert, nil
			}
			tlsCert, err := localproxy.LocalLeafCertificate(s.tlsCA, []string{host})
			if err != nil {
				return nil, err
			}
			actual, _ := s.tlsCerts.LoadOrStore(host, tlsCert)
			cached := actual.(tls.Certificate)
			return &cached, nil
		},
	}
}

func (s *Server) agentTLSHostAllowed(host string) bool {
	host = normalizeRouteRequestHost(host)
	if host == "localhost" {
		return true
	}
	return s.hasRouteHost(host)
}

func listenRouter(addr string) (net.Listener, string, error) {
	ln, err := net.Listen("tcp", addr)
	if err == nil {
		return ln, ln.Addr().String(), nil
	}
	if strings.TrimSpace(addr) != defaultRouterAddr {
		return nil, "", fmt.Errorf("listen scenery agent router at %s failed; choose a different --router-listen address or free that port: %w", addr, err)
	}
	ln, fallbackErr := net.Listen("tcp", "127.0.0.1:0")
	if fallbackErr != nil {
		return nil, "", err
	}
	slog.Warn("scenery agent router default port unavailable; using fallback", "addr", ln.Addr().String(), "err", err)
	return ln, ln.Addr().String(), nil
}

func listenUnixSocket(path string) (net.Listener, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("agent socket path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := removeStaleSocket(path); err != nil {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	return ln, nil
}

func removeStaleSocket(path string) error {
	client := NewClient(path)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := client.Ping(ctx); err == nil {
		return fmt.Errorf("scenery agent already listening at %s", path)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Server) writeState() error {
	state := State{
		SchemaVersion:    StateSchemaVersion,
		PID:              os.Getpid(),
		Identity:         s.identity,
		SocketPath:       s.paths.SocketPath,
		RouterAddr:       s.routerAddr,
		PublicRouterAddr: s.publicRouterAddr,
		RouterScheme:     s.routerScheme,
		Edge:             s.edge,
		UpdatedAt:        time.Now().UTC(),
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWriteFile(s.paths.StatePath, data, 0o644)
}

func (s *Server) controlMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", s.handleHealth)
	mux.HandleFunc("/v1/tls/allow", s.handleTLSAllow)
	mux.HandleFunc("/v1/sessions", s.handleSessions)
	mux.HandleFunc("/v1/sessions/", s.handleSession)
	mux.HandleFunc("/v1/substrates", s.handleSubstrates)
	mux.HandleFunc("/v1/substrates/", s.handleSubstrate)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	writeJSON(w, http.StatusOK, HealthResponse{
		SchemaVersion:        StateSchemaVersion,
		PID:                  os.Getpid(),
		Identity:             s.identity,
		SocketPath:           s.paths.SocketPath,
		RouterAddr:           s.routerAddr,
		PublicRouterAddr:     s.publicRouterAddr,
		RouterScheme:         s.routerScheme,
		InternalRouterScheme: s.internalRouterScheme,
		Edge:                 s.edge,
		DashboardBackend:     s.dashboard,
	})
}

func (s *Server) handleTLSAllow(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	domain := normalizeRouteRequestHost(req.URL.Query().Get("domain"))
	if domain == "" || !s.tlsAllowedHost(domain) {
		http.NotFound(w, req)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSubstrates(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, SubstratesResponse{Substrates: s.registry.ListSubstrates()})
	case http.MethodPost:
		defer req.Body.Close()
		var upsert UpsertSubstrateRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<20)).Decode(&upsert); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		substrate, err := s.registry.UpsertSubstrate(upsert)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, SubstrateResponse{Substrate: substrate})
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleSubstrate(w http.ResponseWriter, req *http.Request) {
	kind := strings.Trim(strings.TrimPrefix(req.URL.Path, "/v1/substrates/"), "/")
	if kind == "" {
		http.NotFound(w, req)
		return
	}
	switch req.Method {
	case http.MethodGet:
		substrate, ok := s.registry.GetSubstrate(kind)
		if !ok {
			http.NotFound(w, req)
			return
		}
		writeJSON(w, http.StatusOK, SubstrateResponse{Substrate: substrate})
	case http.MethodDelete:
		substrate, ok, err := s.registry.DeleteSubstrate(kind)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !ok {
			http.NotFound(w, req)
			return
		}
		writeJSON(w, http.StatusOK, SubstrateResponse{Substrate: substrate})
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodDelete)
	}
}

func (s *Server) handleSessions(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		sessions := s.registry.List()
		if appRoot := strings.TrimSpace(req.URL.Query().Get("app_root")); appRoot != "" {
			sessions = s.registry.FindByAppRoot(appRoot)
		}
		writeJSON(w, http.StatusOK, StatusResponse{Sessions: sessions})
	case http.MethodPost:
		defer req.Body.Close()
		var register RegisterRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<20)).Decode(&register); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		session, err := s.registry.Upsert(register)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, RegisterResponse{Session: session})
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleSession(w http.ResponseWriter, req *http.Request) {
	id := strings.Trim(strings.TrimPrefix(req.URL.Path, "/v1/sessions/"), "/")
	if id == "" {
		http.NotFound(w, req)
		return
	}
	switch req.Method {
	case http.MethodGet:
		session, ok := s.registry.Get(id)
		if !ok {
			http.NotFound(w, req)
			return
		}
		writeJSON(w, http.StatusOK, RegisterResponse{Session: session})
	case http.MethodDelete:
		ownerPID := 0
		ownerStrict := false
		owner := Owner{}
		if raw := strings.TrimSpace(req.URL.Query().Get("owner_pid")); raw != "" {
			if raw == "none" {
				ownerPID = -1
			} else {
				parsed, err := strconv.Atoi(raw)
				if err != nil || parsed <= 0 {
					http.Error(w, "owner_pid must be a positive integer or none", http.StatusBadRequest)
					return
				}
				ownerPID = parsed
			}
		}
		if req.URL.Query().Get("owner_strict") == "1" {
			ownerStrict = true
		}
		if ownerPID > 0 {
			owner = Owner{
				PID:         ownerPID,
				StartedAt:   strings.TrimSpace(req.URL.Query().Get("owner_started_at")),
				CmdlineHash: strings.TrimSpace(req.URL.Query().Get("owner_cmdline_hash")),
				Exe:         strings.TrimSpace(req.URL.Query().Get("owner_exe")),
			}
		}
		session, ok, err := s.registry.DeleteOwnedIdentity(id, ownerPID, owner, ownerStrict)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !ok && session.SessionID == "" {
			http.NotFound(w, req)
			return
		}
		if ok && req.URL.Query().Get("signal") == "1" && (session.OwnerPID > 0 || session.Owner.PID > 0) {
			owner, err := ownerForSignal(session.OwnerPID, session.Owner)
			if err != nil {
				slog.Warn("skipping scenery up owner interrupt because owner fingerprint did not verify", "pid", firstPositive(session.Owner.PID, session.OwnerPID), "err", err)
			} else if err := interruptProcess(owner.PID); err != nil {
				slog.Warn("failed to interrupt scenery up owner", "pid", owner.PID, "err", err)
			}
		}
		writeJSON(w, http.StatusOK, RegisterResponse{Session: session, Deleted: ok})
	default:
		methodNotAllowed(w, http.MethodGet, http.MethodDelete)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func methodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
