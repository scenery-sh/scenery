package edge

import (
	"fmt"
	"net"
	"path/filepath"
	"sort"
	"strings"

	localagent "scenery.sh/internal/agent"
)

// CaddyConfigOptions describes one rendered Caddyfile for the managed edge
// process: the private HTTPS listener, the agent-router upstream, and any
// publicly served ACME domains.
type CaddyConfigOptions struct {
	ListenAddr     string
	PublicPort     string
	Upstream       string
	AskURL         string
	AdminSocket    string
	Token          string
	PublicDomains  []PublicDomainSite
	ACMEEmail      string
	ACMECA         string
	StorageDir     string
	HTTPListenPort string
}

// PublicDomainSite is one publicly served domain in the edge Caddyfile.
type PublicDomainSite struct {
	Domain string
}

// CaddyConfig renders the managed edge Caddyfile for the given options.
func CaddyConfig(opts CaddyConfigOptions) string {
	host, listenPort := splitHostPort(opts.ListenAddr)
	if host == "" {
		host = "127.0.0.1"
	}
	if listenPort == "" {
		listenPort = "19443"
	}
	publicPort := strings.TrimSpace(opts.PublicPort)
	if publicPort == "" {
		publicPort = "443"
	}
	httpPort := strings.TrimSpace(opts.HTTPListenPort)
	if httpPort == "" {
		httpPort = "19080"
	}
	site := "https://:" + listenPort
	publicDomains := normalizedPublicDomainSites(opts.PublicDomains)
	var b strings.Builder
	fmt.Fprintf(&b, `{
	default_bind %s
	auto_https disable_redirects
`, host)
	if len(publicDomains) == 0 {
		b.WriteString("	local_certs\n")
	}
	if opts.AskURL != "" {
		fmt.Fprintf(&b, `	on_demand_tls {
		ask %s
	}
`, opts.AskURL)
	}
	fmt.Fprintf(&b, "	admin unix//%s\n", opts.AdminSocket)
	if storage := strings.TrimSpace(opts.StorageDir); storage != "" {
		fmt.Fprintf(&b, "	storage file_system %s\n", storage)
	}
	if email := strings.TrimSpace(opts.ACMEEmail); email != "" {
		fmt.Fprintf(&b, "	email %s\n", email)
	}
	if len(publicDomains) > 0 {
		fmt.Fprintf(&b, "	http_port %s\n", httpPort)
		fmt.Fprintf(&b, "	https_port %s\n", listenPort)
	}
	b.WriteString(`	servers {
		strict_sni_host on
	}
}

`)
	// lb_try_duration bridges the agent router's supervised restart window:
	// a refused upstream dial retries for a bounded time instead of exposing
	// a raw 502 on the public edge.
	fmt.Fprintf(&b, `%s {
	tls internal {
		on_demand
	}
	reverse_proxy %s {
		flush_interval -1
		lb_try_duration 5s
		lb_try_interval 250ms
		header_up Host {host}
		header_up X-Forwarded-Proto https
		header_up X-Forwarded-Port %s
		header_up X-Scenery-Edge-Token %s
	}
}
`, site, opts.Upstream, publicPort, opts.Token)
	for _, site := range publicDomains {
		fmt.Fprintf(&b, `
%s:%s {
	tls {
		issuer acme%s
	}
	reverse_proxy %s {
		flush_interval -1
		lb_try_duration 5s
		lb_try_interval 250ms
		header_up Host {host}
		header_up X-Forwarded-Proto https
		header_up X-Forwarded-Port 443
		header_up X-Scenery-Edge-Token %s
		header_up X-Scenery-Public-Edge 1
	}
}

http://%s:%s {
	redir https://{host}{uri} 308
}
`, site.Domain, listenPort, caddyACMEIssuerOptions(opts.ACMECA), opts.Upstream, opts.Token, site.Domain, httpPort)
	}
	return b.String()
}

// CaddyConfigForRegistry renders the managed edge Caddyfile with the public
// ACME sites taken from the agent home's deploy registry.
func CaddyConfigForRegistry(paths localagent.Paths, targetAddr, httpTargetAddr, upstreamAddr, adminSocket, token string) (string, error) {
	deployRegistry, err := localagent.LoadDeployRegistry(paths.DeployPath)
	if err != nil {
		return "", err
	}
	publicDomains := publicDomainSitesForDeployRegistry(deployRegistry)
	_, httpPort := splitHostPort(httpTargetAddr)
	storageDir := ""
	if len(publicDomains) > 0 {
		storageDir = filepath.Join(paths.EdgeDir, "caddy-data")
	}
	return CaddyConfig(CaddyConfigOptions{
		ListenAddr:     targetAddr,
		PublicPort:     "443",
		Upstream:       upstreamAddr,
		AskURL:         "http://" + upstreamAddr + "/v1/tls/allow",
		AdminSocket:    adminSocket,
		Token:          token,
		PublicDomains:  publicDomains,
		ACMEEmail:      deployRegistry.ACMEEmail,
		ACMECA:         deployRegistry.ACMECA,
		StorageDir:     storageDir,
		HTTPListenPort: httpPort,
	}), nil
}

func normalizedPublicDomainSites(sites []PublicDomainSite) []PublicDomainSite {
	seen := map[string]bool{}
	out := make([]PublicDomainSite, 0, len(sites))
	for _, site := range sites {
		domain := strings.ToLower(strings.TrimSpace(site.Domain))
		if domain == "" || seen[domain] {
			continue
		}
		seen[domain] = true
		out = append(out, PublicDomainSite{Domain: domain})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Domain < out[j].Domain
	})
	return out
}

func publicDomainSitesForDeployRegistry(registry localagent.DeployRegistry) []PublicDomainSite {
	sites := make([]PublicDomainSite, 0, len(registry.Targets))
	for _, target := range registry.Targets {
		if !target.Enabled {
			continue
		}
		sites = append(sites, PublicDomainSite{Domain: target.Domain})
	}
	return normalizedPublicDomainSites(sites)
}

func caddyACMEIssuerOptions(ca string) string {
	switch strings.TrimSpace(ca) {
	case "staging":
		return ` {
			ca https://acme-staging-v02.api.letsencrypt.org/directory
		}`
	default:
		return ""
	}
}

func splitHostPort(addr string) (string, string) {
	host, port, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return "", ""
	}
	return host, port
}
