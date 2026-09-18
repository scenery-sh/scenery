package generate

import (
	"fmt"
	"sort"
	"strings"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/mcpcontract"
	"scenery.sh/internal/mcpprojection"
)

func canonicalAssistantResources(resources []Resource) []Resource {
	seen := map[string]bool{}
	assistants := make([]Resource, 0)
	for _, resource := range resources {
		if resource.Kind != "scenery.assistant" || strings.TrimSpace(resource.Address) == "" || seen[resource.Address] {
			continue
		}
		seen[resource.Address] = true
		assistants = append(assistants, resource)
	}
	sort.Slice(assistants, func(i, j int) bool { return assistants[i].Address < assistants[j].Address })
	return assistants
}

func assistantRuntimeRevision(result *Result, address string) string {
	if result == nil {
		return compiler.DefaultAssistantRuntimeRevision
	}
	return compiler.AssistantRuntimeRevision(result.ImplementationRevisions, address)
}

func assistantSurface(assistant Resource) (map[string]any, error) {
	surface, ok := assistant.Spec["surface"].(map[string]any)
	if !ok || surface == nil {
		return nil, fmt.Errorf("assistant %s has no surface", assistant.Address)
	}
	return surface, nil
}

func assistantRuntimeAccess(assistant Resource) (string, error) {
	surface, err := assistantSurface(assistant)
	if err != nil {
		return "", err
	}
	if refOrString(surface["authentication"]) == "std.authentication.none" {
		return "sceneryruntime.Public", nil
	}
	return "sceneryruntime.Auth", nil
}

// renderAssistantHTTPPolicy reuses the HTTP gateway profile renderer while
// sourcing authentication, authorization, and pipeline from the assistant's
// public surface. Assistant surfaces do not have their own request/response
// limits or timeout profile, so the referenced gateway supplies those fields.
func renderAssistantHTTPPolicy(resources map[string]Resource, assistant Resource) (string, error) {
	surface, err := assistantSurface(assistant)
	if err != nil {
		return "", err
	}
	gatewayAddress := resolveResourceRef(assistant, refOrString(surface["gateway"]), "http_gateway")
	gateway, ok := resources[gatewayAddress]
	if !ok || gateway.Kind != "scenery.http-gateway" {
		return "", fmt.Errorf("assistant %s references unknown HTTP gateway %q", assistant.Address, gatewayAddress)
	}
	// renderContractHTTPPolicy expects a binding-shaped owner for resolving
	// auth/pipeline references. Keep this synthetic owner local to generation;
	// no provider or adapter metadata enters the generated registration.
	owner := Resource{Address: assistant.Address, Module: assistant.Module, Spec: map[string]any{
		"gateway":       map[string]any{"$ref": gatewayAddress},
		"authorization": surface["authorization"],
		"pipeline":      surface["pipeline"],
	}}
	return renderContractHTTPPolicy(resources, owner, gateway.Spec), nil
}

func renderAssistantRegistration(result *Result, resources map[string]Resource, assistant Resource) (string, error) {
	surface, err := assistantSurface(assistant)
	if err != nil {
		return "", err
	}
	access, err := assistantRuntimeAccess(assistant)
	if err != nil {
		return "", err
	}
	policy, err := renderAssistantHTTPPolicy(resources, assistant)
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(stringValue(surface["path"]))
	if path == "" {
		path = "/assistants/" + assistant.Name
	}
	name := strings.TrimSpace(assistant.Name)
	if name == "" {
		name = assistantNameFromCanonicalAddress(assistant.Address)
	}
	// Every declared assistant is required by default. The runtime bootstrap
	// still fails closed to a neutral unavailable public surface when its
	// supervisor-supplied private helper descriptor is absent or unhealthy.
	// One resolution of the declared reference serves generation, the
	// development runtime and the artifact build: the server address is part of
	// the capability revision every one of them must agree on.
	serverAddress, err := mcpprojection.AssistantServerAddress(result.Manifest, assistant.Address)
	if err != nil {
		return "", fmt.Errorf("assistant %s MCP server: %w", assistant.Address, err)
	}
	server, ok := resources[serverAddress]
	if !ok || server.Kind != "scenery.mcp-server" {
		return "", fmt.Errorf("assistant %s references unknown MCP server %q", assistant.Address, serverAddress)
	}
	manifest, err := mcpprojection.ProjectManifest(result.Manifest, result.WorkspaceRevision, serverAddress)
	if err != nil {
		return "", fmt.Errorf("assistant %s MCP manifest: %w", assistant.Address, err)
	}
	capabilityRevision, err := mcpprojection.CapabilityRevision(result.Manifest, assistant.Address, serverAddress)
	if err != nil {
		return "", fmt.Errorf("assistant %s capability revision: %w", assistant.Address, err)
	}
	manifest.ContractRevision = capabilityRevision
	manifestJSON, err := mcpcontract.MarshalCanonical(manifest)
	if err != nil {
		return "", fmt.Errorf("assistant %s MCP manifest encoding: %w", assistant.Address, err)
	}
	registration := fmt.Sprintf("if err := sceneryruntime.RegisterAssistantChecked(sceneryruntime.AssistantRegistration{Address: %q, Name: %q, Path: %q, Access: %s, Policy: %s, AssistantAddress: %q, RuntimeRevision: %q, CapabilityRevision: %q, Required: true}); err != nil { return err }\n", assistant.Address, name, path, access, policy, assistant.Address, assistantRuntimeRevision(result, assistant.Address), capabilityRevision)
	registration += fmt.Sprintf("if err := sceneryruntime.RegisterAssistantMCPManifestChecked(%q, %q, []byte(%q)); err != nil { return err }\n", assistant.Address, serverAddress, string(manifestJSON))
	return registration, nil
}

func assistantNameFromCanonicalAddress(address string) string {
	address = strings.TrimSuffix(strings.TrimSpace(address), "/")
	if index := strings.LastIndexByte(address, '/'); index >= 0 {
		address = address[index+1:]
	}
	return address
}
