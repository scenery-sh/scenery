package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type RunOptions struct {
	SocketPath string
	RouterAddr string
	JSON       bool
}

type Server struct {
	paths      Paths
	registry   *Registry
	routerAddr string
	control    *http.Server
	router     *http.Server
	controlLn  net.Listener
	routerLn   net.Listener
}

func Run(ctx context.Context, opts RunOptions) error {
	server, err := NewServer(opts)
	if err != nil {
		return err
	}
	return server.Run(ctx)
}

func NewServer(opts RunOptions) (*Server, error) {
	paths, err := DefaultPaths()
	if err != nil {
		return nil, err
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
	routerLn, actualRouterAddr, err := listenRouter(routerAddr)
	if err != nil {
		return nil, err
	}
	registry, err := OpenRegistry(paths.RegistryPath, actualRouterAddr)
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
		paths:      paths,
		registry:   registry,
		routerAddr: actualRouterAddr,
		controlLn:  controlLn,
		routerLn:   routerLn,
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
		if err := s.router.Serve(s.routerLn); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
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
	if s.paths.SocketPath != "" {
		if err := os.Remove(s.paths.SocketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func listenRouter(addr string) (net.Listener, string, error) {
	ln, err := net.Listen("tcp", addr)
	if err == nil {
		return ln, ln.Addr().String(), nil
	}
	if strings.TrimSpace(addr) != defaultRouterAddr {
		return nil, "", err
	}
	ln, fallbackErr := net.Listen("tcp", "127.0.0.1:0")
	if fallbackErr != nil {
		return nil, "", err
	}
	slog.Warn("onlava agent router default port unavailable; using fallback", "addr", ln.Addr().String(), "err", err)
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
		return fmt.Errorf("onlava agent already listening at %s", path)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Server) writeState() error {
	state := State{
		SchemaVersion: StateSchemaVersion,
		PID:           os.Getpid(),
		SocketPath:    s.paths.SocketPath,
		RouterAddr:    s.routerAddr,
		UpdatedAt:     time.Now().UTC(),
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
	mux.HandleFunc("/v1/sessions", s.handleSessions)
	mux.HandleFunc("/v1/sessions/", s.handleSession)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	writeJSON(w, http.StatusOK, HealthResponse{
		SchemaVersion: StateSchemaVersion,
		PID:           os.Getpid(),
		SocketPath:    s.paths.SocketPath,
		RouterAddr:    s.routerAddr,
	})
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
		session, ok, err := s.registry.Delete(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !ok {
			http.NotFound(w, req)
			return
		}
		if req.URL.Query().Get("signal") == "1" && session.OwnerPID > 0 {
			if err := interruptProcess(session.OwnerPID); err != nil {
				slog.Warn("failed to interrupt onlava dev owner", "pid", session.OwnerPID, "err", err)
			}
		}
		writeJSON(w, http.StatusOK, RegisterResponse{Session: session})
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

func methodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
