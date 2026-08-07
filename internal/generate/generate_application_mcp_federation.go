package generate

import (
	"fmt"
	"sort"
	"strings"
)

// mcpFederationConnection is the private, deployment-facing projection of
// one canonical mcp_connection as referenced by an mcp_server.  It is kept
// separate from mcpcontract.Manifest and all public client generation: URL,
// auth, timeout, and symbolic secret data must never cross that boundary.
type mcpFederationConnection struct {
	Address        string
	Namespace      string
	URL            string
	Required       bool
	Allow          []string
	Block          []string
	ConnectTimeout int64
	CallTimeout    int64
	RefreshTTL     int64
	AuthScheme     string
	AuthHeader     string
	SecretAddress  string
	SecretStore    string
	SecretKey      string
}

type mcpFederationTarget struct {
	Server             Resource
	AssistantAddresses []string
	LocalToolNames     []string
	MaxInputBytes      int
	MaxResultBytes     int
	Connections        []mcpFederationConnection
	CoveredAddresses   []string
}

// canonicalMCPServers returns only the canonical expanded mcp_server
// resources.  Generation callers pass result.Manifest (the expanded graph),
// and no source/effective projection is consulted here.
func canonicalMCPServers(resources []Resource) []Resource {
	seen := map[string]bool{}
	servers := make([]Resource, 0)
	for _, resource := range resources {
		if resource.Kind != "scenery.mcp-server" || strings.TrimSpace(resource.Address) == "" || seen[resource.Address] {
			continue
		}
		seen[resource.Address] = true
		servers = append(servers, resource)
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].Address < servers[j].Address })
	return servers
}

func mcpFederationTargets(result *Result) ([]mcpFederationTarget, error) {
	if result == nil || result.Manifest == nil {
		return nil, nil
	}
	resources := result.Manifest.Resources
	byAddress := resourcesByAddress(result.Manifest)
	servers := canonicalMCPServers(resources)
	if len(servers) == 0 {
		return nil, nil
	}
	assistantsByServer := map[string][]string{}
	for _, assistant := range canonicalAssistantResources(resources) {
		serverAddress := resolveResourceRef(assistant, refString(assistant.Spec["mcp_server"]), "mcp_server")
		if serverAddress == "" {
			continue
		}
		assistantsByServer[serverAddress] = append(assistantsByServer[serverAddress], assistant.Address)
	}
	for serverAddress := range assistantsByServer {
		assistantsByServer[serverAddress] = canonicalStrings(assistantsByServer[serverAddress])
	}

	targets := make([]mcpFederationTarget, 0, len(servers))
	for _, server := range servers {
		maxInput, ok := integerValue(server.Spec["max_input_bytes"])
		if !ok || maxInput <= 0 {
			return nil, fmt.Errorf("MCP server %s has invalid max_input_bytes", server.Address)
		}
		maxResult, ok := integerValue(server.Spec["max_result_bytes"])
		if !ok || maxResult <= 0 {
			return nil, fmt.Errorf("MCP server %s has invalid max_result_bytes", server.Address)
		}

		localTools := make([]string, 0)
		for _, capability := range namedChildren(server.Spec, "capability") {
			name := strings.TrimSpace(stringValue(capability["name"]))
			if name != "" {
				localTools = append(localTools, name)
			}
		}
		localTools = canonicalStrings(localTools)

		connections := make([]mcpFederationConnection, 0)
		covered := []string{server.Address}
		seenConnections := map[string]bool{}
		for _, child := range namedChildren(server.Spec, "connection") {
			address := resolveResourceRef(server, refString(child["connection"]), "mcp_connection")
			connection, ok := byAddress[address]
			if !ok || connection.Kind != "scenery.mcp-connection" {
				return nil, fmt.Errorf("MCP server %s references unknown mcp_connection %q", server.Address, address)
			}
			if seenConnections[connection.Address] {
				return nil, fmt.Errorf("MCP server %s references mcp_connection %s more than once", server.Address, connection.Address)
			}
			seenConnections[connection.Address] = true
			projected, err := projectMCPFederationConnection(child, connection, byAddress)
			if err != nil {
				return nil, err
			}
			connections = append(connections, projected)
			covered = append(covered, connection.Address)
		}
		sort.Slice(connections, func(i, j int) bool {
			if connections[i].Address != connections[j].Address {
				return connections[i].Address < connections[j].Address
			}
			return connections[i].Namespace < connections[j].Namespace
		})
		covered = canonicalStrings(covered)
		targets = append(targets, mcpFederationTarget{
			Server: server, AssistantAddresses: canonicalStrings(assistantsByServer[server.Address]),
			LocalToolNames: localTools, MaxInputBytes: maxInput, MaxResultBytes: maxResult,
			Connections: connections, CoveredAddresses: covered,
		})
	}
	return targets, nil
}

func projectMCPFederationConnection(child map[string]any, connection Resource, byAddress map[string]Resource) (mcpFederationConnection, error) {
	auth, _ := connection.Spec["auth"].(map[string]any)
	scheme := strings.TrimSpace(stringValue(auth["scheme"]))
	if scheme == "" {
		scheme = "none"
	}
	projected := mcpFederationConnection{
		Address:        connection.Address,
		Namespace:      strings.TrimSpace(stringValue(child["namespace"])),
		URL:            strings.TrimSpace(stringValue(connection.Spec["url"])),
		Required:       child["required"] == true,
		ConnectTimeout: durationNanos(connection.Spec["connect_timeout"]),
		CallTimeout:    durationNanos(connection.Spec["call_timeout"]),
		// RefreshTTL is deployment-time runtime policy. The current source
		// contract has no authored field, so leave the zero value and let the
		// federation apply its fixed conservative default.
		RefreshTTL: 0,
		AuthScheme: scheme,
		AuthHeader: strings.TrimSpace(stringValue(auth["header"])),
	}
	tools, _ := connection.Spec["tools"].(map[string]any)
	projected.Allow = canonicalStrings(stringValues(tools["allow"]))
	projected.Block = canonicalStrings(stringValues(tools["block"]))
	secretReference := refString(auth["secret"])
	if secretReference != "" {
		secretAddress := resolveResourceRef(connection, secretReference, "secret")
		secret, ok := byAddress[secretAddress]
		if !ok || secret.Kind != "scenery.secret" {
			return mcpFederationConnection{}, fmt.Errorf("MCP connection %s references unknown secret %q", connection.Address, secretAddress)
		}
		storeAddress := resolveResourceRef(secret, refString(secret.Spec["store"]), "secret_store")
		if strings.TrimSpace(storeAddress) == "" || strings.TrimSpace(stringValue(secret.Spec["key"])) == "" {
			return mcpFederationConnection{}, fmt.Errorf("MCP connection %s references a secret without a symbolic store and key", connection.Address)
		}
		projected.SecretAddress = secret.Address
		projected.SecretStore = storeAddress
		projected.SecretKey = strings.TrimSpace(stringValue(secret.Spec["key"]))
	}
	return projected, nil
}

func renderMCPFederationRegistrations(result *Result, b *strings.Builder) error {
	targets, err := mcpFederationTargets(result)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return nil
	}
	covered := make([]string, 0)
	for _, target := range targets {
		covered = append(covered, target.CoveredAddresses...)
	}
	covered = canonicalStrings(covered)
	fmt.Fprintf(b, "\tif err := registry.Register(%q, sceneryruntime.ContractRegistration{\n", "scenery/mcp-federation")
	fmt.Fprintf(b, "\t\tContractRevision: ContractRevision, PackageContractABIRevision: ContractRevision, RuntimeABI: sceneryruntime.ContractRuntimeABI, CoveredAddresses: %#v,\n", covered)
	b.WriteString("\t\tApply: func() error {\n")
	for _, target := range targets {
		fmt.Fprintf(b, "\t\t\tif err := sceneryruntime.RegisterMCPFederationChecked(sceneryruntime.MCPFederationRegistration{Address: %q, AssistantAddresses: %#v, CapabilityRevision: ContractRevision, LocalToolNames: %#v, MaxInputBytes: %d, MaxResultBytes: %d, Connections: []sceneryruntime.MCPConnectionSpec{\n", target.Server.Address, target.AssistantAddresses, target.LocalToolNames, target.MaxInputBytes, target.MaxResultBytes)
		for _, connection := range target.Connections {
			fmt.Fprintf(b, "\t\t\t\t{Address: %q, Namespace: %q, URL: %q, Required: %t, Allow: %#v, Block: %#v, ConnectTimeout: %d, CallTimeout: %d, RefreshTTL: %d, AuthScheme: %q, AuthHeader: %q, Secret: sceneryruntime.MCPSecretReference{ResourceAddress: %q, StoreAddress: %q, Key: %q}},\n", connection.Address, connection.Namespace, connection.URL, connection.Required, connection.Allow, connection.Block, connection.ConnectTimeout, connection.CallTimeout, connection.RefreshTTL, connection.AuthScheme, connection.AuthHeader, connection.SecretAddress, connection.SecretStore, connection.SecretKey)
		}
		b.WriteString("\t\t\t}}); err != nil { return err }\n")
	}
	b.WriteString("\t\t\treturn nil\n\t\t},\n\t}); err != nil { return err }\n")
	return nil
}
