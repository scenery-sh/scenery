package main

import (
	"fmt"

	cp "scenery.sh/internal/clientprojection"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/mcpcontract"
	"scenery.sh/internal/mcpprojection"
	workerruntime "scenery.sh/runtime"
)

type workerAssistantPlan struct {
	Address            string                           `json:"address"`
	Path               string                           `json:"path"`
	MCPServer          string                           `json:"mcp_server"`
	Authentication     string                           `json:"authentication"`
	SessionAccess      string                           `json:"session_access"`
	RuntimeRevision    string                           `json:"runtime_revision"`
	CapabilityRevision string                           `json:"capability_revision"`
	Policy             workerruntime.ContractHTTPPolicy `json:"policy"`
}

func (bundle *workerBundle) projectAssistants(result *compiler.Result) error {
	projection := workerPlanProjection{resources: cp.ResourcesByAddress(result.Manifest)}
	for _, resource := range result.Manifest.Resources {
		switch resource.Kind {
		case "scenery.mcp-server":
			manifest, err := mcpprojection.ProjectManifest(result.Manifest, result.WorkspaceRevision, resource.Address)
			if err != nil {
				return err
			}
			if bundle.Host.MCPServers == nil {
				bundle.Host.MCPServers = make(map[string]mcpcontract.Manifest)
			}
			bundle.Host.MCPServers[resource.Address] = manifest
		case "scenery.assistant":
			plan, err := projection.assistant(result, resource)
			if err != nil {
				return err
			}
			bundle.Host.Assistants = append(bundle.Host.Assistants, plan)
		}
	}
	return nil
}

func (p workerPlanProjection) assistant(result *compiler.Result, resource graph.Resource) (workerAssistantPlan, error) {
	surface, ok := resource.Spec["surface"].(map[string]any)
	if !ok {
		return workerAssistantPlan{}, fmt.Errorf("assistant %s has no surface", resource.Address)
	}
	server, err := mcpprojection.AssistantServerAddress(result.Manifest, resource.Address)
	if err != nil {
		return workerAssistantPlan{}, err
	}
	capability, err := mcpprojection.CapabilityRevision(result.Manifest, resource.Address, server)
	if err != nil {
		return workerAssistantPlan{}, err
	}
	owner := resource
	owner.Spec = surface
	policy := p.policy(workerruntime.WorkerServicePlan{Name: resource.Name}, owner, graph.Resource{})
	policy.GatewayAddress = workerReference(owner, surface["gateway"], "http_gateway")
	gateway := p.resources[policy.GatewayAddress]
	policy.CORS = cp.LastRef(workerRefText(gateway.Spec["cors"]))
	if value, ok := gateway.Spec["cors"].(map[string]any); ok {
		policy.AllowedOrigins = workerStrings(value["origins"])
	}
	return workerAssistantPlan{
		Address: resource.Address, Path: firstNonEmpty(cp.StringValue(surface["path"]), "/assistants/"+resource.Name),
		MCPServer: server, Authentication: workerReference(owner, surface["authentication"], "authentication"),
		SessionAccess: cp.StringValue(surface["session_access"]), Policy: policy,
		RuntimeRevision: assistantRuntimeRevisionFor(result, resource.Address), CapabilityRevision: capability,
	}, nil
}
