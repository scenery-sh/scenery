package generate

import "scenery.sh/internal/machine"

const (
	goApplicationDescriptorKind   = "scenery.generated"
	goPackageDescriptorKind       = "scenery.package-generated"
	typeScriptDescriptorKind      = "scenery.typescript-client-generated"
	goApplicationSchemaDescriptor = machine.ExactSchemaRevision("sha256:e5ec71660dc6740e8fa94a5e0c924f604c696225e4b1799d2908a9b3a5419f3d")
	goPackageSchemaDescriptor     = machine.ExactSchemaRevision("sha256:fa325f0aa48e8489c7ae85ab996efe2bd50962483ba9a3fcfc0e162384ffdca0")
	typeScriptSchemaDescriptor    = machine.ExactSchemaRevision("sha256:02994dd4a50ed41bdbeab0ba09020de6d9b8366353b2d44bd7022981ca17e801")
)

func addGeneratedArtifactIdentity(values map[string]any, kind string, descriptor any, specRevision string) map[string]any {
	identity := machine.NewArtifactIdentity(kind, descriptor)
	if specRevision != "" {
		identity.SpecRevision = specRevision
	}
	values["kind"] = identity.Kind
	values["schema_revision"] = identity.SchemaRevision
	values["spec_revision"] = identity.SpecRevision
	values["producer"] = identity.Producer
	return values
}
