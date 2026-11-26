package main

import (
	"fmt"
	"sort"
	"strings"

	cp "scenery.sh/internal/clientprojection"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/mcpcontract"
	"scenery.sh/internal/mcpprojection"
	workerruntime "scenery.sh/runtime"
)

func projectWorkerAssistants(result *compiler.Result) ([]workerruntime.WorkerAssistantPlan, error) {
	resources := cp.ResourcesByAddress(result.Manifest)
	seen := make(map[string]bool)
	var assistants []graph.Resource
	for _, resource := range result.Manifest.Resources {
		if resource.Kind != "scenery.assistant" || strings.TrimSpace(resource.Address) == "" || seen[resource.Address] {
			continue
		}
		seen[resource.Address] = true
		assistants = append(assistants, resource)
	}
	if len(assistants) == 0 {
		return nil, nil
	}
	sort.Slice(assistants, func(i, j int) bool { return assistants[i].Address < assistants[j].Address })
	projection := workerPlanProjection{resources: resources}
	plans := make([]workerruntime.WorkerAssistantPlan, 0, len(assistants))
	for _, assistant := range assistants {
		plan, err := projection.assistant(result, assistant)
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

func (p workerPlanProjection) assistant(result *compiler.Result, assistant graph.Resource) (workerruntime.WorkerAssistantPlan, error) {
	surface, ok := assistant.Spec["surface"].(map[string]any)
	if !ok || surface == nil {
		return workerruntime.WorkerAssistantPlan{}, fmt.Errorf("assistant %s has no surface", assistant.Address)
	}
	gatewayAddress := workerReference(assistant, surface["gateway"], "http_gateway")
	gateway, ok := p.resources[gatewayAddress]
	if !ok || gateway.Kind != "scenery.http-gateway" {
		return workerruntime.WorkerAssistantPlan{}, fmt.Errorf("assistant %s references unknown HTTP gateway %q", assistant.Address, gatewayAddress)
	}
	name := strings.TrimSpace(assistant.Name)
	if name == "" {
		name = assistantNameFromAddress(assistant.Address)
	}
	owner := graph.Resource{
		Address: assistant.Address,
		Module:  assistant.Module,
		Spec: map[string]any{
			"protocol":      "http",
			"gateway":       map[string]any{"$ref": gatewayAddress},
			"authorization": surface["authorization"],
			"pipeline":      surface["pipeline"],
		},
	}
	policy := p.policy(workerruntime.WorkerServicePlan{Name: name}, owner, graph.Resource{})
	populateWorkerGatewayHTTPPolicy(&policy, gateway, gateway.Spec)
	serverAddress, err := mcpprojection.AssistantServerAddress(result.Manifest, assistant.Address)
	if err != nil {
		return workerruntime.WorkerAssistantPlan{}, fmt.Errorf("assistant %s MCP server: %w", assistant.Address, err)
	}
	server, ok := p.resources[serverAddress]
	if !ok || server.Kind != "scenery.mcp-server" {
		return workerruntime.WorkerAssistantPlan{}, fmt.Errorf("assistant %s references unknown MCP server %q", assistant.Address, serverAddress)
	}
	manifest, err := mcpprojection.ProjectManifest(result.Manifest, result.WorkspaceRevision, serverAddress)
	if err != nil {
		return workerruntime.WorkerAssistantPlan{}, fmt.Errorf("assistant %s MCP manifest: %w", assistant.Address, err)
	}
	capabilityRevision, err := mcpprojection.CapabilityRevision(result.Manifest, assistant.Address, serverAddress)
	if err != nil {
		return workerruntime.WorkerAssistantPlan{}, fmt.Errorf("assistant %s capability revision: %w", assistant.Address, err)
	}
	manifest.ContractRevision = capabilityRevision
	manifestJSON, err := mcpcontract.MarshalCanonical(manifest)
	if err != nil {
		return workerruntime.WorkerAssistantPlan{}, fmt.Errorf("assistant %s MCP manifest encoding: %w", assistant.Address, err)
	}
	return workerruntime.WorkerAssistantPlan{
		Address:            assistant.Address,
		Name:               name,
		Path:               workerAssistantPath(assistant),
		Policy:             policy,
		RuntimeRevision:    assistantRuntimeRevisionFor(result, assistant.Address),
		CapabilityRevision: capabilityRevision,
		MCPManifest:        manifestJSON,
	}, nil
}

func workerAssistantPath(assistant graph.Resource) string {
	surface, _ := assistant.Spec["surface"].(map[string]any)
	if path := strings.TrimSpace(cp.StringValue(surface["path"])); path != "" {
		return path
	}
	name := strings.TrimSpace(assistant.Name)
	if name == "" {
		name = assistantNameFromAddress(assistant.Address)
	}
	return "/assistants/" + name
}
