package localproxy

import (
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

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

func DiscoverFrontendUpstream(appRoot string, frontend FrontendConfig) string {
	if override := FrontendOverride(frontend.Name); override != "" {
		return override
	}
	if upstream := normalizeUpstream(frontend.Upstream); upstream != "" {
		return upstream
	}
	frontendRoot := frontendRootPath(appRoot, frontend)
	if frontendRoot == "" {
		return ""
	}
	for _, path := range []string{
		filepath.Join(frontendRoot, "vite.config.ts"),
		filepath.Join(frontendRoot, "vite.config.js"),
		filepath.Join(frontendRoot, "vite.config.mts"),
		filepath.Join(frontendRoot, "vite.config.mjs"),
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		port := parseVitePort(data)
		if port == 0 {
			port = 5173
		}
		return discoverReachableLoopbackUpstream(port)
	}
	return ""
}

func ResolveFrontends(appRoot string, frontends []FrontendConfig) []FrontendConfig {
	resolved := make([]FrontendConfig, 0, len(frontends))
	for _, frontend := range frontends {
		frontend.Name = sanitizeLabel(frontend.Name)
		frontend.Host = normalizeHost(frontend.Host)
		frontend.Root = strings.TrimSpace(frontend.Root)
		frontend.Upstream = DiscoverFrontendUpstream(appRoot, frontend)
		if frontend.Upstream == "" {
			continue
		}
		resolved = append(resolved, frontend)
	}
	return resolved
}

func frontendRootPath(appRoot string, frontend FrontendConfig) string {
	appRoot = strings.TrimSpace(appRoot)
	root := strings.TrimSpace(frontend.Root)
	if root == "" && frontend.Name != "" {
		root = filepath.Join("apps", frontend.Name)
	}
	if root == "" {
		return ""
	}
	if filepath.IsAbs(root) {
		return filepath.Clean(root)
	}
	if appRoot == "" {
		return filepath.Clean(root)
	}
	return filepath.Join(appRoot, root)
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

func sanitizeLabel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = invalidLabelRE.ReplaceAllString(value, "-")
	value = repeatedDashRE.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-")
	return value
}

func parseVitePort(data []byte) int {
	matches := vitePortRE.FindSubmatch(data)
	if len(matches) != 2 {
		return 0
	}
	port, err := strconv.Atoi(string(matches[1]))
	if err != nil {
		return 0
	}
	return port
}

func discoverReachableLoopbackUpstream(port int) string {
	portStr := strconv.Itoa(port)
	for _, candidate := range []string{
		net.JoinHostPort("::1", portStr),
		net.JoinHostPort("127.0.0.1", portStr),
		net.JoinHostPort("localhost", portStr),
	} {
		conn, err := netDialTimeout("tcp", candidate, 150*time.Millisecond)
		if err != nil {
			continue
		}
		_ = conn.Close()
		return candidate
	}
	return net.JoinHostPort("localhost", portStr)
}
