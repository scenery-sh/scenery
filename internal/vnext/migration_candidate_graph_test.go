package vnext

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrationCandidateValidationKeepsActiveNativeAndVerifiedLegacyBindings(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"audit", "house"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeNestedModuleFile(t, filepath.Join(root, "scenery.scn"), `language {
  edition = "2027"
  require_profiles = ["scenery.compiler-core/v1", "scenery.inspection-core/v1"]
}
workspace { managed_generated_roots = ["internal/scenerygen"] }
go_module "application" {
  root = "."
  import_path = "example.test/candidate-links"
}
application "candidate_links" { version = "1.0.0" }
module "audit" { source = "./audit" }
module "house" {
  source = "./house"
  inputs = { audit_binding = module.audit.binding }
}
`)
	writeNestedModuleFile(t, filepath.Join(root, "audit", "scenery.package.scn"), `package "audit" {
  version = "1.0.0"
  scenery_version = ">= 2.0.0, < 3.0.0"
  go_contract { import_path = "example.test/candidate-links/audit" }
}
service "audit" {
  runtime = "go"
  implementation { constructor = "NewService" }
}
operation "ping" {
  service = service.audit
  input = std.type.unit
  handler {
    method = "Ping"
    adapter = "fixture"
  }
  result "ok" { type = std.type.unit }
}
execution "ping_direct" {
  operation = operation.ping
  mode = "direct"
  timeout = "30s"
}
binding "ping_internal" {
  operation = operation.ping
  execution = execution.ping_direct
  protocol = "internal"
  delivery = "call"
  exposure = "application"
  authentication = std.authentication.inherit
  authorization = std.authorization.public
  pipeline = std.pipeline.empty
  internal {
    visibility = "application"
    principal = "inherit"
  }
}
export "binding" { value = binding.ping_internal }
`)
	writeNestedModuleFile(t, filepath.Join(root, "house", "scenery.package.scn"), `package "house" {
  version = "1.0.0"
  scenery_version = ">= 2.0.0, < 3.0.0"
  go_contract { import_path = "example.test/candidate-links/house" }
}
input "audit_binding" { type = resource_ref("binding") }
service "house" {
  runtime = "go"
  implementation { constructor = "NewService" }
  client "audit" { binding = var.audit_binding }
}
`)
	compiled, err := Compile(root)
	if err != nil || !compiled.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, compiled.Diagnostics)
	}
	service := resourcesByAddress(compiled.Manifest)["house/service/house"]
	clients := namedChildren(service.Spec, "client")
	if len(clients) != 1 || refString(clients[0]["binding"]) != "audit/binding/ping_internal" {
		t.Fatalf("resolved cross-service client = %#v", clients)
	}

	for _, owner := range []string{"native", "verified_legacy"} {
		t.Run(owner, func(t *testing.T) {
			active := cloneResourceView(compiled.Manifest.Resources)
			if owner == "verified_legacy" {
				for index := range active {
					if active[index].Module != "audit" {
						continue
					}
					active[index].Origin.Kind = "legacy_v0"
					active[index].Compatibility = &LegacyCompatibility{Semantics: "legacy_exact", Contract: "verified", MigrationDisposition: "native_equivalent"}
				}
			}
			candidate := make([]Resource, 0)
			for _, resource := range active {
				if resource.Module == "house" {
					candidate = append(candidate, resource)
				}
			}
			migration := &Migration{
				Services:         []MigrationService{{Name: "house", State: "shadow", Active: "native"}},
				LegacyCandidates: map[string][]Resource{},
				NativeCandidates: map[string][]Resource{"house": candidate},
			}
			validateMigrationCandidateGraphs(root, active, migration)
			if !migration.Services[0].NativeCandidateValid {
				t.Fatalf("%s binding candidate diagnostics = %#v", owner, migration.Services[0].CandidateDiagnostics["native"])
			}
			if owner == "verified_legacy" {
				binding := resourcesByAddress(&Manifest{Resources: active})["audit/binding/ping_internal"]
				if binding.Origin.Kind != "legacy_v0" || binding.Compatibility == nil || binding.Compatibility.Contract != "verified" {
					t.Fatalf("legacy binding evidence = %#v", binding)
				}
			}
		})
	}
}
