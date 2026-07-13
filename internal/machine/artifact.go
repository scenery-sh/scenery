package machine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"runtime/debug"
	"strings"

	"scenery.sh/internal/spec"
)

// DecodeArtifact accepts exactly one current artifact JSON value. Callers may
// treat any error as a cache miss for disposable state; durable state must
// surface the error or migrate it explicitly.
func DecodeArtifact(encoded []byte, target any, identity *ArtifactIdentity, kind string, schemaDescriptor any, action string) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return err
	}
	if identity == nil {
		return fmt.Errorf("missing %s artifact identity", kind)
	}
	return ValidateArtifactIdentity(*identity, kind, schemaDescriptor, action)
}

// ArtifactIdentity is the common header for disposable cross-process artifacts.
// SchemaRevision identifies the exact JSON shape; SpecRevision binds the
// artifact to the compiler contract that produced it.
type ArtifactIdentity struct {
	Kind           string   `json:"kind"`
	SchemaRevision string   `json:"schema_revision"`
	SpecRevision   string   `json:"spec_revision"`
	Producer       Producer `json:"producer"`
}

// ExactSchemaRevision binds an artifact to a complete checked JSON Schema.
// Use it only with a static revision proved against that schema in tests;
// structural descriptors remain appropriate for private artifacts without a
// checked schema document.
type ExactSchemaRevision string

func NewArtifactIdentity(kind string, schemaDescriptor any) ArtifactIdentity {
	schemaRevision := ArtifactSchemaRevision(schemaDescriptor)
	if _, ok := schemaDescriptor.(ExactSchemaRevision); ok && !isCanonicalDigest(schemaRevision) {
		panic("machine: invalid exact schema revision for " + kind)
	}
	return ArtifactIdentity{
		Kind:           kind,
		SchemaRevision: schemaRevision,
		SpecRevision:   string(spec.CurrentRevision()),
		Producer:       RuntimeProducer(),
	}
}

func ArtifactSchemaRevision(schemaDescriptor any) string {
	if exact, ok := schemaDescriptor.(ExactSchemaRevision); ok {
		return string(exact)
	}
	return string(spec.SchemaRevision(schemaDescriptor))
}

func ValidateArtifactIdentity(identity ArtifactIdentity, kind string, schemaDescriptor any, action string) error {
	if identity.Kind != kind {
		return fmt.Errorf("unexpected artifact kind %q; %s with the current Scenery CLI", identity.Kind, action)
	}
	wantSchema := ArtifactSchemaRevision(schemaDescriptor)
	if _, ok := schemaDescriptor.(ExactSchemaRevision); ok && !isCanonicalDigest(wantSchema) {
		return fmt.Errorf("invalid exact schema revision for %s; %s with the current Scenery CLI", kind, action)
	}
	if identity.SchemaRevision != wantSchema {
		return fmt.Errorf("unexpected %s schema revision %q; %s with the current Scenery CLI", kind, identity.SchemaRevision, action)
	}
	wantSpec := string(spec.CurrentRevision())
	if identity.SpecRevision != wantSpec {
		return fmt.Errorf("unexpected %s spec revision %q; %s with the current Scenery CLI", kind, identity.SpecRevision, action)
	}
	if strings.TrimSpace(identity.Producer.Version) == "" || strings.TrimSpace(identity.Producer.Toolchain.GoVersion) == "" {
		return fmt.Errorf("invalid %s producer identity; %s with the current Scenery CLI", kind, action)
	}
	return nil
}

func isCanonicalDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

// RuntimeProducer returns the best producer identity available to library code.
// The CLI envelope replaces these values with linker-supplied release metadata.
func RuntimeProducer() Producer {
	producer := Producer{Version: "dev", Toolchain: Toolchain{GoVersion: runtime.Version()}}
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			producer.Version = info.Main.Version
		}
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				producer.Commit = setting.Value
			case "vcs.time":
				producer.BuiltAt = setting.Value
			}
		}
	}
	return producer
}
