package compiler

const (
	deploymentProviderABI    = "scenery.deployment-provider/v1"
	providerDescriptorKind   = "scenery.provider-descriptor"
	providerSchemaDescriptor = `{"kind":"scenery.provider-descriptor","schema_revision":"digest","spec_revision":"digest","producer":"producer","source":"string","capabilities":["string"],"config_schema":"object","instance_kinds":{"*":{"capabilities":["string"],"lifecycles":["string"]}},"runtime_abi":"string","deployment_abi":"string","migration_abi":"optional_string"}`
)
