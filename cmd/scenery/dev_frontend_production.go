package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/localproxy"
)

// staticFrontendServer serves a production-built frontend bundle in-process
// on a hidden loopback port. It is registered as an ordinary frontend
// backend: routing, allowed hosts, and domain dispatch see no difference
// from a dev server, there is just no HMR and no child process.
type staticFrontendServer struct {
	Name     string
	Root     string
	Dir      string
	BasePath string
	Addr     string

	buildBin  string
	buildArgs []string
	buildEnv  []string
	logFile   *os.File

	mu     sync.Mutex
	ready  atomic.Bool
	server *http.Server
}

// startProductionFrontendServer allocates the backend address immediately
// and runs the first build asynchronously; the returned channel reports the
// build outcome the way dev-server readiness is reported. Requests arriving
// before the first build completes answer 503.
func startProductionFrontendServer(appRoot string, frontend localproxy.FrontendConfig, baseEnv []string, session localagent.Session) (*managedFrontendProcess, <-chan error, error) {
	root := managedFrontendRoot(appRoot, frontend)
	if root == "" {
		return nil, nil, fmt.Errorf("frontend %q has no root", frontend.Name)
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return nil, nil, fmt.Errorf("frontend root %s is not a directory", root)
	}
	basePath := managedFrontendBasePath(session, frontend.Name)
	buildBin, buildArgs, err := managedFrontendBuildCommand(root, basePath)
	if err != nil {
		return nil, nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}
	addr := listener.Addr().String()
	logFile, err := managedFrontendLogFile(session, frontend.Name)
	if err != nil {
		_ = listener.Close()
		return nil, nil, err
	}
	static := &staticFrontendServer{
		Name:      frontend.Name,
		Root:      root,
		Dir:       filepath.Join(root, "dist"),
		BasePath:  basePath,
		Addr:      addr,
		buildBin:  buildBin,
		buildArgs: buildArgs,
		buildEnv:  frontendDevEnv(baseEnv, appRoot, addr, session, frontend.Name),
		logFile:   logFile,
	}
	static.server = &http.Server{Handler: static.handler()}
	go func() {
		_ = static.server.Serve(listener)
	}()
	ready := make(chan error, 1)
	go func() {
		defer close(ready)
		if err := static.Rebuild(context.Background()); err != nil {
			ready <- fmt.Errorf("build frontend %q: %w", frontend.Name, err)
			return
		}
		ready <- nil
	}()
	return &managedFrontendProcess{
		Name:    frontend.Name,
		Root:    root,
		Addr:    addr,
		LogFile: logFile,
		Static:  static,
	}, ready, nil
}

// Rebuild runs the frontend build script and marks the server ready on
// success. Builds are serialized; the output directory is rebuilt in place,
// so a brief window may serve a mid-write bundle — acceptable for the dev
// runtime, recorded in ExecPlan 0117.
func (s *staticFrontendServer) Rebuild(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cmd := exec.CommandContext(ctx, s.buildBin, s.buildArgs...)
	cmd.Dir = s.Root
	cmd.Env = s.buildEnv
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	if s.logFile != nil {
		_, _ = s.logFile.Write(output.Bytes())
	}
	if err != nil {
		return fmt.Errorf("%s %s: %w\n%s", s.buildBin, strings.Join(s.buildArgs, " "), err, tailLines(output.String(), 20))
	}
	if info, statErr := os.Stat(s.Dir); statErr != nil || !info.IsDir() {
		return fmt.Errorf("build produced no output directory at %s", s.Dir)
	}
	s.ready.Store(true)
	return nil
}

func (s *staticFrontendServer) Close() error {
	if s == nil || s.server == nil {
		return nil
	}
	return s.server.Close()
}

func (s *staticFrontendServer) handler() http.Handler {
	fileServer := http.FileServer(http.Dir(s.Dir))
	prefix := strings.TrimSuffix(strings.TrimSpace(s.BasePath), "/")
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			methodNotAllowedResponse(w)
			return
		}
		if !s.ready.Load() {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "frontend build in progress", http.StatusServiceUnavailable)
			return
		}
		requestPath := path.Clean("/" + req.URL.Path)
		if prefix != "" && prefix != "/" {
			switch {
			case requestPath == prefix:
				requestPath = "/"
			case strings.HasPrefix(requestPath, prefix+"/"):
				requestPath = requestPath[len(prefix):]
			}
		}
		if !staticFrontendFileExists(s.Dir, requestPath) && staticFrontendNavigationRequest(requestPath) && staticFrontendFileExists(s.Dir, "/index.html") {
			requestPath = "/"
		}
		req.URL.Path = requestPath
		fileServer.ServeHTTP(w, req)
	})
}

func methodNotAllowedResponse(w http.ResponseWriter) {
	w.Header().Set("Allow", "GET, HEAD")
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

// staticFrontendNavigationRequest reports an HTML navigation path eligible
// for the SPA index fallback: no file extension on the final segment.
func staticFrontendNavigationRequest(requestPath string) bool {
	return path.Ext(requestPath) == ""
}

func staticFrontendFileExists(dir, requestPath string) bool {
	file, err := http.Dir(dir).Open(requestPath)
	if err != nil {
		return false
	}
	info, err := file.Stat()
	_ = file.Close()
	if err != nil {
		return false
	}
	if !info.IsDir() {
		return true
	}
	index, err := http.Dir(dir).Open(path.Join(requestPath, "index.html"))
	if err != nil {
		return false
	}
	_ = index.Close()
	return true
}

func tailLines(value string, n int) string {
	lines := strings.Split(strings.TrimRight(value, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
