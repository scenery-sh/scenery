// Package generateapi is the stdlib-only generate leaf: shared types,
// runtime-integration plans and assistant-asset
// descriptors. Heavy codegen stays in internal/generate.
package generateapi

// GoWorkspaceProjection shares one rendered artifact set between private
// workspace preparation and native package analysis for that preparation.
type GoWorkspaceProjection struct {
	Files map[string][]byte
}

// RuntimeIntegrationPlan lists the Worker ABI v1 process of every native Go
// service so codegen can render one entrypoint per service.
type RuntimeIntegrationPlan struct {
	ContractRevision string
	Services         []ServiceProcessPlan
}

// ServiceProcessPlan identifies the generated adapter a service process
// registers and the exact contract resources that process must cover.
type ServiceProcessPlan struct {
	Address string
	Name    string
	// ContractRevision is the service contract revision the adapter
	// implements (compiler.ServiceContractRevision).
	ContractRevision  string
	AdapterImport     string
	RequiredAddresses []string
}
