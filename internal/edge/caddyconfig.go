package edge

import (
	"fmt"
	"net"
	"path/filepath"
	"runtime"
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
	// PublicDirect binds public domain sites straight to 0.0.0.0:443/:80
	// instead of the loopback ports behind the macOS privileged forwarder.
	// This is the Linux/systemd public edge mode.
	PublicDirect bool
}

// PublicDomainSite is one publicly served domain in the edge Caddyfile.
type PublicDomainSite struct {
	Domain string
	// Frontends are published production frontends served directly from
	// disk by Caddy. Dynamic routes keep the agent-proxy behavior.
	Frontends []StaticFrontendRoute
}

// StaticFrontendRoute serves one published production frontend. Root is the
// directory Caddy serves (normally the publication `current` symlink);
// OwnsRoot serves the frontend only at `/`; non-root frontends use /<name>/.
type StaticFrontendRoute struct {
	Name     string
	Root     string
	OwnsRoot bool
}

// publicDirectDefault selects direct public binding for platforms without the
// privileged loopback forwarder. Overridable in tests.
var publicDirectDefault = func() bool { return runtime.GOOS == "linux" }

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
		if opts.PublicDirect {
			b.WriteString("	http_port 80\n")
			b.WriteString("	https_port 443\n")
		} else {
			fmt.Fprintf(&b, "	http_port %s\n", httpPort)
			fmt.Fprintf(&b, "	https_port %s\n", listenPort)
		}
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
	for _, domainSite := range publicDomains {
		writePublicDomainSite(&b, domainSite, opts, listenPort, httpPort)
	}
	return b.String()
}

// writePublicDomainSite renders one public ACME domain: Scenery-owned blocked
// paths first, then the dynamic agent proxy for /api/*, then each published
// production frontend from disk, and finally either the root frontend or the
// agent proxy as catch-all. A site without published frontends keeps the
// single unconditional agent proxy.
func writePublicDomainSite(b *strings.Builder, site PublicDomainSite, opts CaddyConfigOptions, listenPort, httpPort string) {
	address := site.Domain + ":" + listenPort
	httpAddress := "http://" + site.Domain + ":" + httpPort
	bind := ""
	if opts.PublicDirect {
		address = site.Domain
		httpAddress = "http://" + site.Domain
		bind = "\tbind 0.0.0.0\n"
	}
	proxy := publicAgentProxy(opts.Upstream, opts.Token)
	fmt.Fprintf(b, "\n%s {\n%s\ttls {\n\t\tissuer acme%s\n\t}\n", address, bind, caddyACMEIssuerOptions(opts.ACMECA))
	frontends := renderableStaticFrontends(site.Frontends)
	if len(frontends) == 0 {
		b.WriteString(indentBlock(proxy, 1))
		b.WriteString("}\n")
	} else {
		b.WriteString(`	@scenery_blocked path /runtime /runtime/* /dashboard /dashboard/* /console /console/* /__scenery /__scenery/*
	handle @scenery_blocked {
		respond "not found" 404
	}
	handle /api/* {
` + indentBlock(proxy, 2) + `	}
`)
		root := StaticFrontendRoute{}
		for _, frontend := range frontends {
			if frontend.OwnsRoot {
				root = frontend
				continue
			}
			fmt.Fprintf(b, "\tredir /%s /%s/ 308\n", frontend.Name, frontend.Name)
			fmt.Fprintf(b, "\thandle_path /%s/* {\n", frontend.Name)
			b.WriteString(staticFrontendBody(frontend))
			b.WriteString("\t}\n")
		}
		if root.Name != "" {
			b.WriteString("\thandle {\n")
			b.WriteString(staticFrontendBody(root))
			b.WriteString("\t}\n")
		} else {
			b.WriteString("\thandle {\n" + indentBlock(proxy, 2) + "\t}\n")
		}
		b.WriteString("}\n")
	}
	fmt.Fprintf(b, `
%s {
%s	redir https://{host}{uri} 308
}
`, httpAddress, bind)
}

// staticFrontendBody renders the shared static file-serving pipeline: GET and
// HEAD only, concrete files first, SPA index fallback only for extensionless
// navigations, immutable caching for content-hashed /assets, and revalidation
// everywhere else. Caddy's file_server keeps validators and byte ranges.
func staticFrontendBody(frontend StaticFrontendRoute) string {
	name := caddyMatcherLabel(frontend.Name)
	return fmt.Sprintf(`		root * %s
		@%s_method not method GET HEAD
		respond @%s_method "method not allowed" 405
		encode zstd gzip
		@%s_fallback {
			not path_regexp \.[A-Za-z0-9]+$
			not file
		}
		rewrite @%s_fallback /index.html
		@%s_immutable path /assets/*
		header @%s_immutable Cache-Control "public, max-age=31536000, immutable"
		@%s_revalidate not path /assets/*
		header @%s_revalidate Cache-Control "no-cache"
		file_server
`, frontend.Root, name, name, name, name, name, name, name, name)
}

func publicAgentProxy(upstream, token string) string {
	return fmt.Sprintf(`reverse_proxy %s {
	flush_interval -1
	lb_try_duration 5s
	lb_try_interval 250ms
	header_up Host {host}
	header_up X-Forwarded-Proto https
	header_up X-Forwarded-Port 443
	header_up X-Scenery-Edge-Token %s
	header_up X-Scenery-Public-Edge 1
}
`, upstream, token)
}

func indentBlock(block string, level int) string {
	prefix := strings.Repeat("\t", level)
	lines := strings.Split(strings.TrimRight(block, "\n"), "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = prefix + line
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

// renderableStaticFrontends drops records that cannot be served safely:
// unsafe names or roots that do not currently resolve to a complete published
// artifact. Dropping falls back to the agent proxy for that route, preserving
// the last known-good public behavior instead of breaking the Caddyfile.
func renderableStaticFrontends(frontends []StaticFrontendRoute) []StaticFrontendRoute {
	out := make([]StaticFrontendRoute, 0, len(frontends))
	seen := map[string]bool{}
	for _, frontend := range frontends {
		name := strings.TrimSpace(frontend.Name)
		root := filepath.Clean(strings.TrimSpace(frontend.Root))
		if name == "" || seen[name] || !safeStaticFrontendName(name) || !filepath.IsAbs(root) {
			continue
		}
		if _, entryPresent, err := CurrentPublishedRelease(root); err != nil || !entryPresent {
			continue
		}
		seen[name] = true
		out = append(out, StaticFrontendRoute{Name: name, Root: root, OwnsRoot: frontend.OwnsRoot})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func safeStaticFrontendName(name string) bool {
	switch name {
	case "api", "runtime", "dashboard", "console", "__scenery":
		return false
	}
	for index, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || (index > 0 && (r == '.' || r == '_' || r == '-')) {
			continue
		}
		return false
	}
	return name != ""
}

func caddyMatcherLabel(name string) string {
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return "fe_" + b.String()
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
		PublicDirect:   publicDirectDefault(),
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
		out = append(out, PublicDomainSite{Domain: domain, Frontends: site.Frontends})
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
		site := PublicDomainSite{Domain: target.Domain}
		for _, frontend := range target.Frontends {
			site.Frontends = append(site.Frontends, StaticFrontendRoute{
				Name:     frontend.Name,
				Root:     frontend.Path,
				OwnsRoot: frontend.Root,
			})
		}
		sites = append(sites, site)
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
