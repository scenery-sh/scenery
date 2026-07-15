package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/edge"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/localproxy"
)

// deployPublishResponse reports one `scenery deploy publish` run: every
// production frontend built and published, whether the edge accepted the new
// configuration, and the direct-origin probe outcome.
type deployPublishResponse struct {
	cliPayloadIdentity
	Domain       string                   `json:"domain"`
	AppID        string                   `json:"app_id"`
	RegistryPath string                   `json:"registry_path"`
	EdgeReloaded bool                     `json:"edge_reloaded"`
	Frontends    []deployPublishFrontend  `json:"frontends"`
	Probe        deployPublishProbeResult `json:"probe"`
}

type deployPublishFrontend struct {
	Name         string `json:"name"`
	Route        string `json:"route"`
	Mode         string `json:"mode"`
	ReleaseID    string `json:"release_id"`
	ArtifactPath string `json:"artifact_path"`
	Files        int    `json:"files"`
	Bytes        int64  `json:"bytes"`
}

type deployPublishProbeResult struct {
	Document string `json:"document"`
	API      string `json:"api"`
}

var deployPublishProbeAddrFunc = deployPublishProbeAddr

// runDeployPublish builds every production-mode frontend of the app, publishes
// the build output into the Scenery-owned deploy artifact store, refreshes the
// deploy registry target, validates and reloads the managed edge, and probes
// the public document and API routes. A validation, reload, or probe failure
// rolls the artifact pointers and registry back to the previous publication.
func runDeployPublish(stdout io.Writer, opts deployOptions) error {
	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	appRoot, cfg, err := appcfg.DiscoverRoot(start)
	if err != nil {
		return err
	}
	domain := strings.TrimSpace(cfg.Deploy.Domain)
	if domain == "" {
		return fmt.Errorf("%s has no deploy.domain; deploy publish serves a configured public domain", cfg.SourcePath(appRoot))
	}
	names := productionFrontendNames(cfg)
	if len(names) == 0 {
		return fmt.Errorf("%s has no frontend with serve: \"production\"; nothing to publish", cfg.SourcePath(appRoot))
	}
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return err
	}
	if err := localagent.EnsureDirs(paths); err != nil {
		return err
	}
	rootService := deployRootService(cfg)
	published := make([]localagent.DeployTargetFrontend, 0, len(names))
	results := make([]deployPublishFrontend, 0, len(names))
	previousReleases := map[string]string{}
	for _, name := range names {
		frontendRoot := managedFrontendRoot(appRoot, localproxy.FrontendConfig{Name: name, Root: cfg.Frontends[name].Root})
		if frontendRoot == "" {
			return fmt.Errorf("frontend %q has no root", name)
		}
		if err := runDeployPublishBuild(frontendRoot, "/"+name, stdout); err != nil {
			return fmt.Errorf("build frontend %q: %w", name, err)
		}
		currentPath := filepath.Join(paths.DeployArtifactsDir, cfg.AppID(), name, "current")
		if target, err := os.Readlink(currentPath); err == nil {
			previousReleases[name] = target
		}
		record, err := edge.PublishFrontendArtifact(edge.PublishInput{
			ArtifactsRoot: paths.DeployArtifactsDir,
			AppID:         cfg.AppID(),
			Frontend:      name,
			SourceDir:     filepath.Join(frontendRoot, "dist"),
		})
		if err != nil {
			return fmt.Errorf("publish frontend %q: %w", name, err)
		}
		route := "/" + name + "/"
		if rootService == name {
			route = "/"
		}
		published = append(published, localagent.DeployTargetFrontend{
			Name:        name,
			Path:        record.CurrentPath,
			Root:        rootService == name,
			ReleaseID:   record.ReleaseID,
			PublishedAt: time.Now().UTC(),
		})
		results = append(results, deployPublishFrontend{
			Name:         name,
			Route:        route,
			Mode:         "caddy_static",
			ReleaseID:    record.ReleaseID,
			ArtifactPath: record.CurrentPath,
			Files:        record.Files,
			Bytes:        record.Bytes,
		})
	}
	registry, err := localagent.LoadDeployRegistry(paths.DeployPath)
	if err != nil {
		return err
	}
	previousRegistry := registry
	if err := upsertDeployTarget(&registry, localagent.DeployTarget{
		Domain:      domain,
		AppRoot:     filepath.Clean(appRoot),
		RootService: rootService,
		Enabled:     true,
		Frontends:   published,
	}); err != nil {
		return err
	}
	if err := localagent.WriteDeployRegistry(paths.DeployPath, registry); err != nil {
		return err
	}
	rollback := func(cause error) error {
		var errs []string
		for _, frontend := range published {
			previous, ok := previousReleases[frontend.Name]
			if !ok {
				continue
			}
			if err := edge.RollbackCurrentRelease(frontend.Path, previous); err != nil {
				errs = append(errs, err.Error())
			}
		}
		if err := localagent.WriteDeployRegistry(paths.DeployPath, previousRegistry); err != nil {
			errs = append(errs, err.Error())
		}
		if err := deployRefreshEdgeAfterMutationFunc(paths); err != nil {
			errs = append(errs, err.Error())
		}
		if len(errs) > 0 {
			return fmt.Errorf("%w; rollback to the previous publication also failed: %s", cause, strings.Join(errs, "; "))
		}
		return fmt.Errorf("%w; rolled back to the previous publication", cause)
	}
	if err := deployRefreshEdgeAfterMutationFunc(paths); err != nil {
		return rollback(fmt.Errorf("reload managed edge: %w", err))
	}
	resp := deployPublishResponse{
		cliPayloadIdentity: newCLIPayloadIdentity("scenery.deploy.publish"),
		Domain:             domain,
		AppID:              cfg.AppID(),
		RegistryPath:       paths.DeployPath,
		EdgeReloaded:       true,
		Frontends:          results,
	}
	resp.Probe = deployPublishProbe(paths, domain)
	if strings.HasPrefix(resp.Probe.Document, "failed") {
		return rollback(fmt.Errorf("public document probe %s", resp.Probe.Document))
	}
	if opts.JSON {
		return writeCLIJSON(stdout, resp)
	}
	for _, frontend := range resp.Frontends {
		fmt.Fprintf(stdout, "published %s -> https://%s%s (%s, %d files)\n", frontend.Name, domain, frontend.Route, frontend.ReleaseID, frontend.Files)
	}
	fmt.Fprintf(stdout, "probe: document %s, api %s\n", resp.Probe.Document, resp.Probe.API)
	return nil
}

func productionFrontendNames(cfg appcfg.Config) []string {
	names := make([]string, 0, len(cfg.Frontends))
	for name, frontend := range cfg.Frontends {
		if strings.TrimSpace(frontend.Serve) == "production" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// runDeployPublishBuild runs the frontend package build with the path-mode
// base prefix, mirroring the dev runtime's production serve mode so one build
// configuration serves both the agent route and the public domain.
func runDeployPublishBuild(frontendRoot, basePath string, stdout io.Writer) error {
	buildBin, buildArgs, err := managedFrontendBuildCommand(frontendRoot, basePath)
	if err != nil {
		return err
	}
	cmd := exec.Command(buildBin, buildArgs...)
	cmd.Dir = frontendRoot
	cmd.Env = envpolicy.Environ()
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w\n%s", buildBin, strings.Join(buildArgs, " "), err, tailLines(output.String(), 20))
	}
	fmt.Fprintf(stdout, "built %s (%s %s)\n", frontendRoot, buildBin, strings.Join(buildArgs, " "))
	return nil
}

// deployPublishProbeAddr picks the local TLS endpoint that terminates the
// public domain: the direct 443 listener under systemd, otherwise the managed
// edge HTTPS listener behind the macOS forwarder.
func deployPublishProbeAddr(paths localagent.Paths) string {
	if edgeSystemdManaged() {
		return "127.0.0.1:443"
	}
	state, err := localagent.LoadEdgeState(paths.EdgeStatePath)
	if err == nil && strings.TrimSpace(state.HTTPSListen) != "" {
		return state.HTTPSListen
	}
	return defaultEdgeTargetAddr
}

// deployPublishProbe fetches the public entry document and an API route
// through the local edge listener with the real domain SNI, isolating
// Scenery/Caddy from DNS and CDN variance.
func deployPublishProbe(paths localagent.Paths, domain string) deployPublishProbeResult {
	addr := deployPublishProbeAddrFunc(paths)
	client := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, addr)
			},
			TLSClientConfig: &tls.Config{ServerName: domain, InsecureSkipVerify: true},
		},
	}
	result := deployPublishProbeResult{}
	if resp, err := client.Get("https://" + domain + "/"); err != nil {
		result.Document = "failed: " + err.Error()
	} else {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		switch {
		case resp.StatusCode != http.StatusOK:
			result.Document = fmt.Sprintf("failed: status %d", resp.StatusCode)
		case bytes.Contains(body, []byte("@vite/client")):
			result.Document = "failed: document still serves the Vite development client"
		default:
			result.Document = "ok"
		}
	}
	if resp, err := client.Get("https://" + domain + "/api"); err != nil {
		result.API = "failed: " + err.Error()
	} else {
		_ = resp.Body.Close()
		if resp.StatusCode >= http.StatusBadGateway {
			result.API = fmt.Sprintf("failed: status %d", resp.StatusCode)
		} else {
			result.API = fmt.Sprintf("ok (status %d)", resp.StatusCode)
		}
	}
	return result
}
