// Package generateapi is the stdlib-only generate leaf: shared types,
// runtime-integration plans and assistant-asset
// descriptors. Heavy codegen stays in internal/generate.
package generateapi

// GoWorkspaceProjection shares one rendered artifact set between private
// workspace preparation and native package analysis for that preparation.
type GoWorkspaceProjection struct {
	Files                map[string][]byte
	VerificationOverlay  map[string][]byte
	VerificationPatterns []string
}

// LibraryBuildSpec is the portable identity of one declared Go library used
// by shared-object builds. Resolution from a compiler result stays in
// internal/generate.
type LibraryBuildSpec struct {
	Address        string
	Name           string
	Artifact       string
	Version        string
	ABIHash        string
	ExportPackage  string
	ExportBuildTag string
}

// RuntimeIntegrationPlan is the generated composition import consumed by
// codegen when preparing a build workspace.
type RuntimeIntegrationPlan struct {
	CompositionImport string
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
