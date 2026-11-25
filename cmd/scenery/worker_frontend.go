package main

import (
	"context"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"

	"scenery.sh/internal/app"
	cp "scenery.sh/internal/clientprojection"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/generate"
	"scenery.sh/internal/staticfrontend"
)

type workerFrontend struct {
	Name     string   `json:"name"`
	Root     string   `json:"root"`
	Reserved []string `json:"reserved"`
}

// requireWorkerFrontends rejects a frontend that the environment serves and
// the worker Host would start without. The Host serves one frontend at the
// application origin: the root frontend, declared as a static Vite build. It
// has no route for another frontend and manages neither a development server
// selected by root alone nor an upstream.
func requireWorkerFrontends(cfg app.Config, envName string) error {
	environment, err := cfg.ResolveEnv(envName)
	if err != nil {
		return err
	}
	root := cfg.RootFrontend()
	for _, name := range slices.Sorted(maps.Keys(environment.Frontends)) {
		if name != root {
			return preconditionErrorf("frontend %q is not served by an application with worker services: its Host serves only the root frontend at the application origin; make %q the root frontend, or set envs.%s.frontends.%s.serve to \"disabled\"", name, name, environment.Name, name)
		}
		if environment.Frontends[name].Adapter != "vite" {
			return preconditionErrorf("frontend %q is not served by an application with worker services: declare frontends.%s with \"adapter\": \"vite\" and \"mode\": \"static\", a workspace-relative root and no upstream", name, name)
		}
	}
	return nil
}

func selectWorkerFrontend(root string, cfg app.Config, result *compiler.Result) (*workerFrontend, error) {
	name := cfg.RootFrontend()
	frontend := cfg.Frontends[name]
	if frontend.Adapter != "vite" {
		return nil, nil
	}
	if !filepath.IsLocal(name) || filepath.Base(name) != name || name == "." {
		return nil, fmt.Errorf("frontend name for Vite must be a single directory name")
	}
	directory := filepath.Join(root, filepath.FromSlash(frontend.Root))
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil || canonical != directory {
		return nil, fmt.Errorf("frontend root for Vite must be an app-relative non-symlink directory")
	}
	reserved := []string{"/api", "/auth", "/__app", "/__scenery"}
	for _, resource := range result.Manifest.Resources {
		if resource.Kind == "scenery.http-gateway" {
			if path := cp.StringValue(resource.Spec["base_path"]); path != "" && path != "/" {
				reserved = append(reserved, path)
			}
		}
	}
	return &workerFrontend{Name: name, Root: directory, Reserved: reserved}, nil
}

func buildWorkerFrontend(ctx context.Context, result *compiler.Result, frontend workerFrontend) (*workerFrontend, error) {
	if _, err := generate.GenerateSnapshotWithContext(ctx, result, "", false); err != nil {
		return nil, err
	}
	program, args, err := managedFrontendBuildCommand(frontend.Root, "/")
	if err != nil {
		return nil, err
	}
	build := exec.CommandContext(ctx, program, args...)
	build.Dir = frontend.Root
	if output, err := build.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("build Vite frontend %s: %w\n%s", frontend.Name, err, tailLines(string(output), 30))
	}
	frontend.Root = filepath.Join(frontend.Root, "dist")
	verified, err := staticfrontend.Open(frontend.Root, frontend.Reserved)
	if err != nil {
		return nil, err
	}
	if err := verified.Close(); err != nil {
		return nil, err
	}
	return &frontend, nil
}

func configureWorkerFrontend(ctx context.Context, root string, cfg app.Config, result *compiler.Result, env string) (http.Handler, func(), error) {
	clean := func() {}
	frontend, err := selectWorkerFrontend(root, cfg, result)
	if err != nil || frontend == nil {
		return nil, clean, err
	}
	resolved, err := cfg.ResolveEnv(env)
	if err != nil {
		return nil, clean, err
	}
	if resolved.Frontends[frontend.Name].Serve == "production" {
		built, err := buildWorkerFrontend(ctx, result, *frontend)
		if err != nil {
			return nil, clean, err
		}
		static, err := staticfrontend.Open(built.Root, built.Reserved)
		if err != nil {
			return nil, clean, err
		}
		return static, func() { _ = static.Close() }, nil
	}
	if _, err := generate.GenerateSnapshotWithContext(ctx, result, "", false); err != nil {
		return nil, clean, err
	}
	return startWorkerVite(ctx, *frontend)
}

func startWorkerVite(ctx context.Context, frontend workerFrontend) (http.Handler, func(), error) {
	clean := func() {}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, clean, err
	}
	address := listener.Addr().String()
	_ = listener.Close()
	_, port, _ := net.SplitHostPort(address)
	program, args, err := managedFrontendCommand(frontend.Root, port, "", "/")
	if err != nil {
		return nil, clean, err
	}
	command := exec.Command(program, append(args, "--strictPort")...)
	command.Dir = frontend.Root
	command.Stdout, command.Stderr = os.Stderr, os.Stderr
	if err := command.Start(); err != nil {
		return nil, clean, err
	}
	done := make(chan struct{})
	go func() { _ = command.Wait(); close(done) }()
	clean = func() {
		process := &workerProcess{cmd: command, done: done}
		process.stop()
	}
	if err := waitWorkerReady(ctx, func(ctx context.Context) error {
		connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
		if err != nil {
			return err
		}
		return connection.Close()
	}); err != nil {
		clean()
		return nil, func() {}, err
	}
	upstream := &url.URL{Scheme: "http", Host: address}
	proxy := &httputil.ReverseProxy{Rewrite: func(request *httputil.ProxyRequest) {
		request.SetURL(upstream)
		request.Out.Host = address
		request.Out.URL.RawQuery = request.In.URL.RawQuery
	}}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if staticfrontend.Reserved(req.URL.Path, frontend.Reserved) {
			staticfrontend.NotFound(w)
			return
		}
		proxy.ServeHTTP(w, req)
	}), clean, nil
}
