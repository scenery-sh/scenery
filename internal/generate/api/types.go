// Package generateapi is the stdlib-only generate leaf: shared types,
// runtime-integration plans and assistant-asset
// descriptors. Heavy codegen stays in internal/generate.
package generateapi

// GoWorkspaceProjection shares one rendered artifact set between private
// workspace preparation and native package analysis for that preparation.
type GoWorkspaceProjection struct {
	Files map[string][]byte
}

// RuntimeIntegrationPlan is the generated composition import consumed by
// codegen when preparing a build workspace. Services lists every native service
// adapter so codegen can render one entrypoint per service process and a host
// entrypoint that routes to them.
type RuntimeIntegrationPlan struct {
	CompositionImport string
	ContractRevision  string
	Services          []ServiceProcessPlan
	// HostApplication is the generated source of the host's application-level
	// registrations (assistants and MCP federation); empty when there are none.
	HostApplication []byte
}

// ServiceProcessPlan identifies the generated adapter a service process
// registers, the exact contract resources that process must cover, and the HTTP
// endpoints its adapter registers.
type ServiceProcessPlan struct {
	Address           string
	Name              string
	AdapterImport     string
	RequiredAddresses []string
	Routes            []ServiceProcessRoute
	MCPTools          []ServiceProcessMCPTool
}

// ServiceProcessMCPTool is one MCP tool a service adapter registers.
type ServiceProcessMCPTool struct {
	AssistantAddress string
	Name             string
}

// ServiceProcessRoute is one runtime HTTP endpoint pattern registered by a
// service adapter.
type ServiceProcessRoute struct {
	Methods  []string
	Path     string
	PathTail bool
}

// AssistantAssetDescriptor is the provider-neutral identity of one
// production assistant asset set.
type AssistantAssetDescriptor struct {
	Kind                 string `json:"kind"`
	SchemaRevision       string `json:"schema_revision"`
	AssistantAddress     string `json:"assistant_address"`
	Target               string `json:"target"`
	RuntimeRevision      string `json:"runtime_revision"`
	CapabilityRevision   string `json:"capability_revision"`
	NodeArchiveDigest    string `json:"node_archive_digest"`
	NodeTreeDigest       string `json:"node_tree_digest"`
	CapsuleArchiveDigest string `json:"capsule_archive_digest"`
	CapsuleTreeDigest    string `json:"capsule_tree_digest"`
	CapsuleEntry         string `json:"capsule_entry"`
	PackageLockDigest    string `json:"package_lock_digest"`
}

// AssistantAssetInput supplies the deterministic bytes embedded in a
// generated assistant asset registry.
type AssistantAssetInput struct {
	Descriptor            AssistantAssetDescriptor
	NodeArchive           []byte
	NodeDescriptorJSON    []byte
	CapsuleArchive        []byte
	CapsuleDescriptorJSON []byte
}

const (
	AssistantAssetDescriptorKind = "scenery.assistant.runtime-assets"
	AssistantAssetCapsuleEntry   = ".scenery/bootstrap.mjs"
)
