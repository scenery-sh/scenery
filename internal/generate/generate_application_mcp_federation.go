package generate

import (
	"fmt"
	"sort"
	"strings"
)

type mcpFederationTarget struct {
	Server             Resource
	AssistantAddresses []string
	LocalToolNames     []string
	MaxInputBytes      int
	MaxResultBytes     int
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

		targets = append(targets, mcpFederationTarget{
			Server: server, AssistantAddresses: canonicalStrings(assistantsByServer[server.Address]),
			LocalToolNames: localTools, MaxInputBytes: maxInput, MaxResultBytes: maxResult,
		})
	}
	return targets, nil
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
		covered = append(covered, target.Server.Address)
	}
	covered = canonicalStrings(covered)
	fmt.Fprintf(b, "\tif err := registry.Register(%q, sceneryruntime.ContractRegistration{\n", "scenery/mcp-federation")
	fmt.Fprintf(b, "\t\tContractRevision: ContractRevision, PackageContractABIRevision: ContractRevision, RuntimeABI: sceneryruntime.ContractRuntimeABI, CoveredAddresses: %#v,\n", covered)
	b.WriteString("\t\tApply: func() error {\n")
	for _, target := range targets {
		fmt.Fprintf(b, "\t\t\tif err := sceneryruntime.RegisterMCPFederationChecked(sceneryruntime.MCPFederationRegistration{Address: %q, AssistantAddresses: %#v, CapabilityRevision: ContractRevision, LocalToolNames: %#v, MaxInputBytes: %d, MaxResultBytes: %d}); err != nil { return err }\n", target.Server.Address, target.AssistantAddresses, target.LocalToolNames, target.MaxInputBytes, target.MaxResultBytes)
	}
	b.WriteString("\t\t\treturn nil\n\t\t},\n\t}); err != nil { return err }\n")
	return nil
}
