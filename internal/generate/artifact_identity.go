package generate

import "scenery.sh/internal/machine"

const (
	goApplicationDescriptorKind   = "scenery.generated"
	goPackageDescriptorKind       = "scenery.package-generated"
	typeScriptDescriptorKind      = "scenery.typescript-client-generated"
	openAPIDescriptorKind         = "scenery.openapi-generated"
	goApplicationSchemaDescriptor = machine.ExactSchemaRevision("sha256:e5ec71660dc6740e8fa94a5e0c924f604c696225e4b1799d2908a9b3a5419f3d")
	goPackageSchemaDescriptor     = machine.ExactSchemaRevision("sha256:fa325f0aa48e8489c7ae85ab996efe2bd50962483ba9a3fcfc0e162384ffdca0")
	typeScriptSchemaDescriptor    = machine.ExactSchemaRevision("sha256:02994dd4a50ed41bdbeab0ba09020de6d9b8366353b2d44bd7022981ca17e801")
	openAPISchemaDescriptor       = machine.ExactSchemaRevision("sha256:17c78b4e01256e5957b1025d94bb9554147154f9eee053b73554a75ed5148ea6")
	openAPISchemaDocument         = `{"$schema":"https://json-schema.org/draft/2020-12/schema","title":"Scenery generated OpenAPI descriptor","type":"object","required":["kind","schema_revision","spec_revision","producer","gateway","contract_revision","http_surface_revision","openapi_revision","openapi_version","content_digest","generator"],"properties":{"kind":{"const":"scenery.openapi-generated"},"schema_revision":{"$ref":"#/$defs/revision"},"spec_revision":{"$ref":"#/$defs/revision"},"producer":{"type":"object"},"gateway":{"type":"string","minLength":1},"contract_revision":{"$ref":"#/$defs/revision"},"http_surface_revision":{"$ref":"#/$defs/revision"},"openapi_revision":{"$ref":"#/$defs/revision"},"openapi_version":{"type":"string","minLength":1},"content_digest":{"$ref":"#/$defs/revision"},"generator":{"type":"string","minLength":1}},"$defs":{"revision":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"}},"additionalProperties":false}`
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
