package runtimeapp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"

	"onlava.com/internal/dbstudio"
	"onlava.com/internal/localproxy"
	"onlava.com/runtime"
)

func init() {
	runtime.RegisterStandaloneDevStarter(startStandaloneDev)
}

type standaloneSession struct {
	mu      sync.Mutex
	proxy   *localproxy.Proxy
	studio  *dbstudio.Instance
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
	if s.studio != nil {
		if err := s.studio.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.proxy != nil {
		if err := s.proxy.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *standaloneSession) setStudio(inst *dbstudio.Instance) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		_ = inst.Close()
		return
	}
	s.studio = inst
}

func startStandaloneDev(ctx context.Context, cfg runtime.AppConfig) (runtime.StandaloneDevSession, runtime.StandaloneDevInfo, error) {
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
			info.MCPBaseURL = routes.MCPBaseURL
			info.FrontendURL = routes.FrontendURL
		}
	}

	if cfg.EnableDBStudio {
		root := appRootFromEnvOrCWD()
		studioCfg, ok, err := dbstudio.Discover(root)
		if err != nil {
			slog.Warn("db studio unavailable", "err", err)
		} else if ok {
			info.DBStudioURL = dbstudio.DefaultURL(dbstudio.DefaultPort)
			go func() {
				inst, startErr := dbstudio.Start(ctx, dbstudio.Options{
					AppRoot: root,
					AppID:   cfg.Name,
					Config:  studioCfg,
					Port:    dbstudio.DefaultPort,
					Stdout:  osStdout(),
					Stderr:  osStderr(),
				})
				if startErr != nil {
					slog.Warn("db studio unavailable", "err", startErr)
					return
				}
				if inst == nil {
					return
				}
				session.setStudio(inst)
			}()
		}
	}

	return session, info, nil
}

func standaloneDevEnabled() bool {
	return os.Getenv("ONLAVA_STANDALONE_DEV") == "1"
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
		MCPHost:           cfg.ProxyMCPHost,
		FrontendHost:      cfg.ProxyFrontendHost,
		APIUpstream:       cfg.ListenAddr,
		DashboardUpstream: strings.TrimSpace(os.Getenv("ONLAVA_DEV_DASHBOARD_ADDR")),
		FrontendUpstream:  localproxy.DiscoverFrontendUpstream(mustGetwd()),
	})
	if proxyCfg.Workspace == "" && proxyCfg.APIHost == "" {
		return nil, nil
	}
	return localproxy.Start(proxyCfg)
}

func standaloneLocalProxyDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ONLAVA_LOCAL_PROXY"))) {
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

func appRootFromEnvOrCWD() string {
	if root := strings.TrimSpace(os.Getenv("ONLAVA_APP_ROOT")); root != "" {
		return root
	}
	return mustGetwd()
}

func osStdout() io.Writer { return os.Stdout }

func osStderr() io.Writer { return os.Stderr }
