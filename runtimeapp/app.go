package runtimeapp

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/localproxy"
	"scenery.sh/runtime"
)

func init() {
	runtime.RegisterStandaloneDevStarter(startStandaloneDev)
}

type standaloneSession struct {
	mu      sync.Mutex
	proxy   *localproxy.Proxy
	stopped bool
}

func (s *standaloneSession) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return nil
	}
	s.stopped = true
	var errs []error
	if s.proxy != nil {
		if err := s.proxy.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func startStandaloneDev(ctx context.Context, cfg runtime.AppConfig) (runtime.StandaloneDevSession, runtime.StandaloneDevInfo, error) {
	_ = ctx
	session := &standaloneSession{}
	info := runtime.StandaloneDevInfo{}

	if standaloneDevEnabled() {
		if proxy, err := startLocalHTTPSProxy(cfg); err != nil {
			slog.Warn("local HTTPS proxy unavailable", "err", err)
		} else if proxy != nil {
			session.proxy = proxy
			routes := proxy.Routes()
			info.APIURL = routes.APIURL
			info.ConsoleURL = routes.ConsoleURL
			info.FrontendURLs = standaloneFrontendURLs(routes)
		}
	}

	return session, info, nil
}

func standaloneDevEnabled() bool {
	return envpolicy.Get("SCENERY_STANDALONE_DEV") == "1"
}

func startLocalHTTPSProxy(cfg runtime.AppConfig) (*localproxy.Proxy, error) {
	if standaloneLocalProxyDisabled() {
		return nil, nil
	}
	workspace := cfg.Workspace
	if workspace == "" {
		workspace = localproxy.DiscoverWorkspace(mustGetwd(), cfg.Name)
	}
	proxyCfg := localproxy.BuildConfig(localproxy.Config{
		Workspace:         workspace,
		APIHost:           cfg.ProxyAPIHost,
		ConsoleHost:       cfg.ProxyConsoleHost,
		TemporalHost:      cfg.ProxyTemporalHost,
		GrafanaHost:       cfg.ProxyGrafanaHost,
		APIUpstream:       cfg.ListenAddr,
		DashboardUpstream: strings.TrimSpace(envpolicy.Get("SCENERY_DEV_DASHBOARD_ADDR")),
		TemporalUpstream:  standaloneTemporalUIUpstream(cfg),
		Frontends:         localproxy.ResolveFrontends(mustGetwd(), runtimeProxyFrontends(cfg.ProxyFrontends)),
	})
	if proxyCfg.Workspace == "" && proxyCfg.APIHost == "" {
		return nil, nil
	}
	return localproxy.Start(proxyCfg)
}

func standaloneTemporalUIUpstream(cfg runtime.AppConfig) string {
	info := runtime.ResolveTemporalConfig(cfg.Name, cfg.Temporal)
	if info.Mode != runtime.DefaultTemporalMode {
		return ""
	}
	host, portText, err := net.SplitHostPort(strings.TrimSpace(info.Address))
	if err != nil {
		return ""
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return ""
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(port+1000))
}

func runtimeProxyFrontends(frontends map[string]runtime.ProxyFrontendConfig) []localproxy.FrontendConfig {
	names := make([]string, 0, len(frontends))
	for name := range frontends {
		names = append(names, name)
	}
	sort.Strings(names)
	resolved := make([]localproxy.FrontendConfig, 0, len(names))
	for _, name := range names {
		frontend := frontends[name]
		resolved = append(resolved, localproxy.FrontendConfig{
			Name:     name,
			Host:     frontend.Host,
			Root:     frontend.Root,
			Upstream: frontend.Upstream,
		})
	}
	return resolved
}

func standaloneFrontendURLs(routes localproxy.Routes) map[string]string {
	if len(routes.Frontends) == 0 {
		return nil
	}
	names := make([]string, 0, len(routes.Frontends))
	for name := range routes.Frontends {
		names = append(names, name)
	}
	sort.Strings(names)
	urls := make(map[string]string, len(names))
	for _, name := range names {
		urls[name] = routes.Frontends[name].URL
	}
	return urls
}

func standaloneLocalProxyDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(envpolicy.Get("SCENERY_LOCAL_PROXY"))) {
	case "0", "false", "no", "off":
		return true
	default:
		return false
	}
}

func mustGetwd() string {
	wd, _ := os.Getwd()
	return wd
}
