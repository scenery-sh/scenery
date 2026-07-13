package compiler

const (
	deploymentProviderABI          = "scenery.deployment-provider/v1"
	deploymentProjectionKind       = "scenery.deployment-projection"
	deploymentProjectionDescriptor = `{"kind":"scenery.deployment-projection","identity":"artifact","deployment":"address","environment":"string","contract_revision":"revision","resources":"projection_resources"}`
	providerDescriptorKind         = "scenery.provider-descriptor"
	providerSchemaDescriptor       = `{"kind":"scenery.provider-descriptor","schema_revision":"digest","spec_revision":"digest","producer":"producer","source":"string","capabilities":["string"],"config_schema":"object","instance_kinds":{"*":{"capabilities":["string"],"lifecycles":["string"]}},"runtime_abi":"string","deployment_abi":"string","migration_abi":"optional_string"}`
)
