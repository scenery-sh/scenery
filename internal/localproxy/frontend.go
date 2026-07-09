package localproxy

import (
	"net"
	"net/url"
	"regexp"
	"strings"

	"scenery.sh/internal/envpolicy"
)

type FrontendConfig struct {
	Name                string
	Host                string
	Root                string
	Upstream            string
	AllowSharedUpstream bool
}

func FrontendOverride(name string) string {
	value := strings.TrimSpace(envpolicy.Get("SCENERY_FRONTEND_" + frontendEnvName(name) + "_ADDR"))
	if value == "" {
		return ""
	}
	return normalizeUpstream(value)
}

func frontendEnvName(name string) string {
	name = strings.ToUpper(strings.TrimSpace(name))
	var b strings.Builder
	lastUnderscore := false
	for _, r := range name {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func normalizeUpstream(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.Contains(value, "://") {
		u, err := url.Parse(value)
		if err == nil && u.Host != "" {
			return normalizeUpstream(u.Host)
		}
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return value
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

func normalizeHost(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	if strings.Contains(value, "://") {
		u, err := url.Parse(value)
		if err == nil && u.Host != "" {
			value = u.Host
		}
	}
	if slash := strings.IndexByte(value, '/'); slash >= 0 {
		value = value[:slash]
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	return strings.Trim(value, "[]")
}

var invalidLabelRE = regexp.MustCompile(`[^a-z0-9-]+`)
var repeatedDashRE = regexp.MustCompile(`-+`)
var vitePortRE = regexp.MustCompile(`(?m)\bport\s*:\s*([0-9]+)\b`)
var netDialTimeout = net.DialTimeout
