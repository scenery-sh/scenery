package edge

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/atomicfile"
	"scenery.sh/internal/machine"
)

const defaultDNSDomain = localagent.DefaultRouteBaseDomain

// dnsResolverFunctionalTimeout bounds the functional lookup probe used to
// accept an externally managed resolver as serving a domain.
var dnsResolverFunctionalTimeout = 300 * time.Millisecond

// DNSState is the durable dnsmasq resolver state persisted under the agent
// home run directory.
type DNSState struct {
	machine.ArtifactIdentity
	Status       string    `json:"status"`
	PID          int       `json:"pid,omitempty"`
	Domain       string    `json:"domain"`
	Listen       string    `json:"listen"`
	Address      string    `json:"address"`
	Executable   string    `json:"executable,omitempty"`
	ConfigPath   string    `json:"config_path,omitempty"`
	LogPath      string    `json:"log_path,omitempty"`
	ResolverPath string    `json:"resolver_path,omitempty"`
	Error        string    `json:"error,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// DNSResolverState reports whether the scoped OS resolver entry for one
// domain is installed and matches the managed dnsmasq listener.
type DNSResolverState struct {
	Installed  bool   `json:"installed"`
	State      string `json:"state"`
	Path       string `json:"path,omitempty"`
	Domain     string `json:"domain,omitempty"`
	Nameserver string `json:"nameserver,omitempty"`
	Port       string `json:"port,omitempty"`
	Message    string `json:"message,omitempty"`
}

// DNSMasqConfig renders the managed dnsmasq configuration that answers every
// listed domain with the given address.
func DNSMasqConfig(domains []string, listen, address string) string {
	host, port := splitHostPort(listen)
	if host == "" {
		host = "127.0.0.1"
	}
	if port == "" {
		port = "53535"
	}
	domains = normalizeDNSDomains(domains)
	if len(domains) == 0 {
		domains = []string{defaultDNSDomain}
	}
	var b strings.Builder
	fmt.Fprintf(&b, `no-daemon
bind-interfaces
listen-address=%s
port=%s
`, host, port)
	for _, domain := range domains {
		fmt.Fprintf(&b, "address=/%s/%s\n", domain, address)
	}
	b.WriteString(`domain-needed
bogus-priv
no-resolv
`)
	return b.String()
}

// DNSConfigDomains returns the deduplicated domain set the managed dnsmasq
// should serve for one requested domain, keeping every domain that already
// has a scenery-managed OS resolver entry.
func DNSConfigDomains(domain string) []string {
	domains := []string{defaultDNSDomain, domain}
	if runtime.GOOS == "darwin" {
		domains = append(domains, managedDNSResolverDomains()...)
	}
	return normalizeDNSDomains(domains)
}

func managedDNSResolverDomains() []string {
	entries, err := os.ReadDir("/etc/resolver")
	if err != nil {
		return nil
	}
	var domains []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		domain := normalizeDNSHost(entry.Name())
		if domain == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/etc/resolver", entry.Name()))
		if err != nil || !strings.Contains(string(data), "Managed by scenery edge dns") {
			continue
		}
		domains = append(domains, domain)
	}
	return domains
}

func normalizeDNSDomains(domains []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, domain := range domains {
		domain = normalizeDNSHost(domain)
		if domain == "" || seen[domain] {
			continue
		}
		seen[domain] = true
		out = append(out, domain)
	}
	sort.Strings(out)
	return out
}

func normalizeDNSHost(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	if scheme := strings.Index(value, "://"); scheme >= 0 {
		value = value[scheme+3:]
	}
	if slash := strings.IndexByte(value, '/'); slash >= 0 {
		value = value[:slash]
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	return strings.Trim(value, "[]")
}

// DNSResolverServesDomain reports whether the resolver at nameserver:port
// functionally answers a probe name under domain with the expected address.
func DNSResolverServesDomain(domain, nameserver, port, address string) bool {
	domain = normalizeDNSHost(domain)
	if domain == "" || nameserver == "" || port == "" {
		return false
	}
	target := net.JoinHostPort(nameserver, port)
	resolver := net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			dialer := net.Dialer{}
			return dialer.DialContext(ctx, network, target)
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), dnsResolverFunctionalTimeout)
	defer cancel()
	hosts, err := resolver.LookupHost(ctx, "scenery-edge-probe."+domain)
	if err != nil {
		return false
	}
	for _, host := range hosts {
		if host == address {
			return true
		}
	}
	return false
}

// DNSConfigServesDomain reports whether the dnsmasq config at path answers
// the given domain.
func DNSConfigServesDomain(path, domain string) bool {
	domain = normalizeDNSHost(domain)
	if path == "" || domain == "" {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	needle := "address=/" + domain + "/"
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), needle) {
			return true
		}
	}
	return false
}

// DNSInstallCommand returns the CLI command that installs edge DNS for the
// given domain.
func DNSInstallCommand(domain string) string {
	if domain == "" || domain == defaultDNSDomain {
		return "scenery system edge dns install"
	}
	return "scenery system edge dns install --domain " + domain
}

// WriteDNSState atomically persists the edge DNS state for the agent home.
func WriteDNSState(paths localagent.Paths, state DNSState) error {
	state.ArtifactIdentity = machine.NewArtifactIdentity("scenery.edge.dns-state", dnsStateDescriptor)
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = time.Now().UTC()
	}
	if err := os.MkdirAll(filepath.Dir(DNSStatePath(paths)), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(DNSStatePath(paths), append(data, '\n'), 0o600, atomicfile.Options{SyncFile: true, SyncDir: true})
}

// LoadDNSState loads the edge DNS state at path, migrating the legacy
// schema-versioned form in place.
func LoadDNSState(path string) (DNSState, error) {
	var state DNSState
	if err := localagent.LoadDurableArtifact(path, &state, &state.ArtifactIdentity, "scenery.edge.dns-state", dnsStateDescriptor, 0o600, func(fields map[string]json.RawMessage) error {
		var version string
		if err := json.Unmarshal(fields["schema_version"], &version); err != nil || version != "scenery.edge.dns.state.v1" {
			return fmt.Errorf("unsupported legacy edge DNS state schema %q", version)
		}
		delete(fields, "schema_version")
		return nil
	}); err != nil {
		return state, err
	}
	return state, nil
}

const dnsStateDescriptor = `{"identity":"artifact","state":"edge-dns-resolver"}`

// DNSConfigPath returns the managed dnsmasq config path for the agent home.
func DNSConfigPath(paths localagent.Paths) string {
	return filepath.Join(paths.EdgeDir, "dnsmasq.conf")
}

// DNSLogPath returns the managed dnsmasq log path for the agent home.
func DNSLogPath(paths localagent.Paths) string {
	return filepath.Join(paths.EdgeDir, "dnsmasq.log")
}

// DNSStatePath returns the edge DNS state path for the agent home.
func DNSStatePath(paths localagent.Paths) string {
	return filepath.Join(paths.RunDir, "edge-dns.json")
}

// DNSResolverStatus inspects the scoped OS resolver entry for domain against
// the managed dnsmasq listen address.
func DNSResolverStatus(domain, listen string) DNSResolverState {
	status := DNSResolverState{
		State:  "unsupported",
		Domain: domain,
	}
	if runtime.GOOS != "darwin" {
		status.Message = "scoped resolver configuration is currently managed on macOS"
		return status
	}
	status.Path = DNSResolverPath(domain)
	host, port := splitHostPort(listen)
	if host == "" {
		host = "127.0.0.1"
	}
	if port == "" {
		port = "53535"
	}
	status.Nameserver = host
	status.Port = port
	data, err := os.ReadFile(status.Path)
	if err != nil {
		status.State = "missing"
		status.Message = "run `scenery system edge dns install`"
		return status
	}
	fields := ParseResolverFile(string(data))
	if fields["domain"] == domain && fields["nameserver"] == host && fields["port"] == port {
		status.Installed = true
		status.State = "installed"
		return status
	}
	status.State = "mismatch"
	status.Message = "resolver file exists but does not match scenery system edge dns"
	return status
}

// ParseResolverFile parses an /etc/resolver style file into keyword/value
// fields, skipping comments and blank lines.
func ParseResolverFile(data string) map[string]string {
	fields := map[string]string{}
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			fields[parts[0]] = parts[1]
		}
	}
	return fields
}

// DNSResolverPath returns the scoped OS resolver file path for domain.
func DNSResolverPath(domain string) string {
	domain = normalizeDNSHost(domain)
	if domain == "" {
		domain = defaultDNSDomain
	}
	return filepath.Join("/etc/resolver", domain)
}

// DNSResolverFile renders the scenery-managed scoped resolver file contents.
func DNSResolverFile(domain, nameserver, port string) string {
	return fmt.Sprintf(`# Managed by scenery edge dns
domain %s
nameserver %s
port %s
`, domain, nameserver, port)
}
