package edge

import (
	"fmt"
	"net"
	"strings"
)

// CaddyConfigOptions describes one rendered Caddyfile for the managed edge
// process: the private HTTPS listener and the agent-router upstream.
type CaddyConfigOptions struct {
	ListenAddr  string
	PublicPort  string
	Upstream    string
	AskURL      string
	AdminSocket string
	Token       string
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
	site := "https://:" + listenPort
	var b strings.Builder
	fmt.Fprintf(&b, `{
	default_bind %s
	auto_https disable_redirects
	local_certs
`, host)
	if opts.AskURL != "" {
		fmt.Fprintf(&b, `	on_demand_tls {
		ask %s
	}
`, opts.AskURL)
	}
	fmt.Fprintf(&b, "	admin unix//%s\n", opts.AdminSocket)
	b.WriteString(`	servers {
		strict_sni_host on
	}
}

`)
	// lb_try_duration bridges the agent router's supervised restart window:
	// a refused upstream dial retries for a bounded time instead of exposing
	// a raw 502 on the edge.
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
	return b.String()
}

// CaddyConfigForEdge renders the managed edge Caddyfile for one target
// listener and agent-router upstream.
func CaddyConfigForEdge(targetAddr, upstreamAddr, adminSocket, token string) string {
	return CaddyConfig(CaddyConfigOptions{
		ListenAddr:  targetAddr,
		PublicPort:  "443",
		Upstream:    upstreamAddr,
		AskURL:      "http://" + upstreamAddr + "/v1/tls/allow",
		AdminSocket: adminSocket,
		Token:       token,
	})
}

func splitHostPort(addr string) (string, string) {
	host, port, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return "", ""
	}
	return host, port
}
