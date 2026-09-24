package compiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// configurationFixture adds environment-configurable deployment inputs to the
// native fixture's house package. rootInputs is appended to the app's module
// inputs; extra is appended to the package source.
func configurationFixture(t *testing.T, rootInputs, extra string) string {
	t.Helper()
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "native"), root)
	rootPath := filepath.Join(root, appFilename)
	rootSource, err := os.ReadFile(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	rootSource = []byte(strings.Replace(string(rootSource), `gateway = http_gateway.public_api`, "gateway = http_gateway.public_api\n"+rootInputs, 1))
	if err := os.WriteFile(rootPath, rootSource, 0o644); err != nil {
		t.Fatal(err)
	}
	packagePath := filepath.Join(root, "house", packageFilename)
	packageSource, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	packageSource = []byte(strings.Replace(string(packageSource), `input "gateway" {
  type = resource_ref("http_gateway")
}`, `input "gateway" {
  type = resource_ref("http_gateway")
}

input "weather_pack_root" {
  type  = optional(host_path)
  phase = "deployment"
}

input "process_concurrency" {
  type    = uint32
  phase   = "deployment"
  default = 2
  minimum = 1
}

input "provider_token" {
  type      = resource_ref("secret")
  phase     = "deployment"
  sensitive = true
}`, 1))
	packageSource = []byte(strings.Replace(string(packageSource), `  implementation {
    constructor = "NewService"
  }`, `  implementation {
    constructor = "NewService"
  }

  config {
    weather_pack_root   = var.weather_pack_root
    process_concurrency = var.process_concurrency
    provider_token      = var.provider_token
  }`, 1))
	packageSource = append(packageSource, []byte(extra)...)
	if err := os.WriteFile(packagePath, packageSource, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestConfigurableDeploymentInputsDeferToTheEnvironment(t *testing.T) {
	parallelVNextIntegrationTest(t)

	result, err := Compile(configurationFixture(t, "    process_concurrency = 4", ""))
	if err != nil || !result.Valid() {
		t.Fatalf("compile deferred configuration: %v diagnostics=%#v", err, result.Diagnostics)
	}
	service := resourcesByAddress(result.Manifest)["house/service/house"]
	config, _ := service.Spec["config"].(map[string]any)
	if _, ok := config["weather_pack_root"]; ok {
		t.Fatalf("unset optional deployment input stayed in config: %#v", config)
	}
	if _, ok := config["provider_token"]; ok {
		t.Fatalf("unset environment secret stayed in config: %#v", config)
	}
	if config["process_concurrency"] == nil {
		t.Fatalf("declared deployment value missing: %#v", config)
	}
	inputs := map[string]string{}
	for _, field := range namedChildren(service.Spec, "config_schema") {
		inputs[stringValue(field["name"])] = stringValue(field["input"])
	}
	if inputs["weather_pack_root"] != "weather_pack_root" || inputs["provider_token"] != "provider_token" {
		t.Fatalf("config schema input names = %#v", inputs)
	}
	if key := ConfigurationKey(service.Module, "weather_pack_root"); key != "house.weather_pack_root" {
		t.Fatalf("configuration key = %q", key)
	}
}

func TestHostPathIsDeploymentOnly(t *testing.T) {
	parallelVNextIntegrationTest(t)

	result, err := Compile(configurationFixture(t, "", `

record "pack_location" {
  field "root" {
    type = host_path
  }
}
`))
	if err != nil {
		t.Fatal(err)
	}
	if !hasDiagnostic(result.Diagnostics, "SCN1213") {
		t.Fatalf("host_path in a record compiled: %#v", result.Diagnostics)
	}
}

func TestHostPathDefaultMustBeAbsolute(t *testing.T) {
	parallelVNextIntegrationTest(t)

	root := configurationFixture(t, "", "")
	packagePath := filepath.Join(root, "house", packageFilename)
	source, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	source = []byte(strings.Replace(string(source), "type  = optional(host_path)\n", "type    = host_path\n  default = \"relative/packs\"\n", 1))
	if err := os.WriteFile(packagePath, source, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	if !hasDiagnostic(result.Diagnostics, "SCN1212") {
		t.Fatalf("relative host_path default compiled: %#v", result.Diagnostics)
	}
}
