package generate

import (
	"os"
	"path/filepath"
	"testing"
)

func writeMinimalGenerationFixture(t *testing.T, root string) {
	t.Helper()
	files := map[string]string{
		"go.mod": "module example.test/nativeapp\n\ngo 1.27.0\n\nrequire scenery.sh v0.0.0\n",
		testAppFilename: `workspace {
  managed_generated_roots = [
		"clients/generated/public_api",
		"house/scenerycontract",
		"internal/scenerygen",
  ]
}

application "nativeapp" {}

http_gateway "public_api" {
  exposure        = "internet"
  base_path       = "/"
  cors            = std.cors.none
  trusted_proxies = std.trusted_proxies.none
  forwarded       = std.forwarded_headers.reject
}

module "house" {
  source = "./house"
}

typescript_client "public_api" {
  gateways    = [http_gateway.public_api]
  package     = "@example/native-client"
  module      = "esm"
  runtime     = "fetch"
  output_root = "clients/generated/public_api"
}
`,
		filepath.Join("house", testPackageFilename): `package "house" {
  go_contract {
    import_path = "example.test/nativeapp/house"
  }
}

record "scene" {
  field "id" {
    type = string
  }
}

export "scene" {
  value = record.scene
}
`,
	}
	for relative, contents := range files {
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func writeMinimalNativeGenerationFixture(t *testing.T, root string) {
	t.Helper()
	writeMinimalGenerationFixture(t, root)
	appendFixtureFile(t, filepath.Join(root, testAppFilename), `
go_module "application" {
  root        = "."
  import_path = "example.test/nativeapp"
}

go_toolchain "application" {
  version     = "1.27.0"
  experiments = []
}

go_target "development" {
  role      = "development"
  platform  = "host"
  toolchain = go_toolchain.application
  module    = go_module.application
  packages  = ["./..."]
  cgo       = "disabled"
}
`)
	appendFixtureFile(t, filepath.Join(root, "house", testPackageFilename), `
service "house" {
  runtime = "go"

  implementation {
    constructor = "NewService"
  }
}

record "inspect_input" {
  field "id" {
    type = string
  }
}

operation "inspect" {
  service = service.house
  input   = record.inspect_input

  handler {
    method = "Inspect"
  }

  result "ok" {
    type = record.scene
  }
}
`)
}

func writeMinimalPredictedGoFixture(t *testing.T, root string) {
	t.Helper()
	files := map[string]string{
		"go.mod": "module example.test/nativeapp\n\ngo 1.27.0\n\nrequire scenery.sh v0.0.0\n",
		testAppFilename: `workspace {
  implementation_root "application" {
    path             = "."
    revision_include = ["**/*.go", "go.mod"]
  }

  managed_generated_roots = [
    "house/scenerycontract",
    "internal/scenerygen",
  ]
}

application "nativeapp" {}

module "house" {
  source = "./house"
}

go_module "application" {
  root        = "."
  import_path = "example.test/nativeapp"
}

go_toolchain "application" {
  version     = "1.27.0"
  experiments = []
}

go_target "development" {
  role      = "development"
  platform  = "host"
  toolchain = go_toolchain.application
  module    = go_module.application
  packages  = ["./..."]
  cgo       = "disabled"
}
`,
		filepath.Join("house", testPackageFilename): `package "house" {
  go_contract {
    import_path = "example.test/nativeapp/house"
  }
}

service "house" {
  runtime = "go"

  implementation {
    constructor = "NewService"
  }
}

record "inspect_input" {
  field "id" {
    type = string
  }
}

record "inspect_result" {
  field "id" {
    type = string
  }
}

operation "inspect" {
  service = service.house
  input   = record.inspect_input

  handler {
    method = "Inspect"
  }

  result "ok" {
    type = record.inspect_result
  }
}
`,
		filepath.Join("house", "service.go"): `package house

import (
	"context"

	contract "example.test/nativeapp/house/scenerycontract"
)

type Service struct{}

func NewService(context.Context, contract.HouseConstructorInput) (*Service, error) {
	return &Service{}, nil
}

func (*Service) Inspect(_ context.Context, input contract.InspectInput) (contract.InspectOutcome, error) {
	return contract.InspectOk{Value: contract.InspectResult{Id: input.Id}}, nil
}
`,
	}
	for relative, contents := range files {
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func appendFixtureFile(t *testing.T, path, contents string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(contents); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
