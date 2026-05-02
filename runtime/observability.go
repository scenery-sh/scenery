package runtime

import (
	"path"
	"strings"

	"onlava.com/runtime/shared"
)

type ObservabilityConfig struct {
	Logs    EndpointFilterConfig
	Tracing EndpointFilterConfig
}

type EndpointFilterConfig struct {
	IncludeEndpoints []string
	ExcludeEndpoints []string
}

func logsEnabledForRequest(req shared.Request) bool {
	global.mu.RLock()
	cfg := global.observability.Logs
	global.mu.RUnlock()
	return endpointFilterAllows(cfg, req)
}

func traceEnabledForRequest(req shared.Request) bool {
	global.mu.RLock()
	cfg := global.observability.Tracing
	global.mu.RUnlock()
	return endpointFilterAllows(cfg, req)
}

func logsEnabledForAuthHandler(handler *AuthHandler) bool {
	if handler == nil {
		return false
	}
	return logsEnabledForRequest(authHandlerRequest(handler))
}

func traceEnabledForAuthHandler(handler *AuthHandler) bool {
	if handler == nil {
		return false
	}
	return traceEnabledForRequest(authHandlerRequest(handler))
}

func endpointFilterAllows(cfg EndpointFilterConfig, req shared.Request) bool {
	if len(cfg.IncludeEndpoints) == 0 && len(cfg.ExcludeEndpoints) == 0 {
		return true
	}
	candidates := requestFilterCandidates(req)
	if len(cfg.IncludeEndpoints) > 0 && !matchesAnyPattern(cfg.IncludeEndpoints, candidates) {
		return false
	}
	if len(cfg.ExcludeEndpoints) > 0 && matchesAnyPattern(cfg.ExcludeEndpoints, candidates) {
		return false
	}
	return true
}

func matchesAnyPattern(patterns, candidates []string) bool {
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		for _, candidate := range candidates {
			if candidate == "" {
				continue
			}
			matched, err := path.Match(pattern, candidate)
			if err == nil && matched {
				return true
			}
			if candidate == pattern {
				return true
			}
		}
	}
	return false
}

func requestFilterCandidates(req shared.Request) []string {
	items := make([]string, 0, 4)
	if req.Service != "" && req.Endpoint != "" {
		items = append(items, req.Service+"."+req.Endpoint)
	}
	if req.Service != "" {
		items = append(items, req.Service)
	}
	if req.Endpoint != "" {
		items = append(items, req.Endpoint)
	}
	if req.Path != "" {
		items = append(items, req.Path)
	}
	return items
}

func authHandlerRequest(handler *AuthHandler) shared.Request {
	return shared.Request{
		Service:  handler.Service,
		Endpoint: handler.Name,
	}
}
