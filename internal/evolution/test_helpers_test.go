package evolution

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/scn"
)

func copyTree(t *testing.T, source, target string) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func containsJSONText(data []byte, want string) bool {
	var value any
	if json.Unmarshal(data, &value) == nil {
		encoded, _ := json.Marshal(value)
		return strings.Contains(string(encoded), want)
	}
	return strings.Contains(string(data), want)
}

func expressionText(value any) string {
	if expression, ok := value.(map[string]any); ok {
		text, _ := expression["$expression"].(string)
		return strings.TrimSpace(text)
	}
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func hasSCNErrors(diagnostics []scn.Diagnostic) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			return true
		}
	}
	return false
}

func init() {
	testCheckPredictedGoContracts = func(*compiler.Result) error { return nil }
	testCheckPredictedTypeScript = func(*compiler.Result) error { return nil }
	testCheckGenerated = func(*compiler.Result) {}
}

func Check(root string) (*Result, error) {
	result, err := compiler.Compile(root)
	if err == nil && testCheckGenerated != nil {
		testCheckGenerated(result)
	}
	return result, err
}

func newMinimalChangeFixture(t *testing.T) (string, *compiler.Result) {
	t.Helper()
	return newAppChangeFixture(t, "")
}

func newAppChangeFixture(t *testing.T, declarations string) (string, *compiler.Result) {
	t.Helper()
	root := t.TempDir()
	writeNestedModuleFile(t, filepath.Join(root, testAppFilename), "application \"change_test\" {}\n"+declarations)
	return root, compileChangeFixture(t, root)
}

func newHouseChangeFixture(t *testing.T, declarations string) (string, *compiler.Result) {
	t.Helper()
	return newAuthoredHouseChangeFixture(t, false, false, declarations)
}

func newHouseGoChangeFixture(t *testing.T, declarations string) (string, *compiler.Result) {
	t.Helper()
	return newAuthoredHouseChangeFixture(t, true, false, declarations)
}

func newHouseGatewayChangeFixture(t *testing.T, declarations string) (string, *compiler.Result) {
	t.Helper()
	return newAuthoredHouseChangeFixture(t, false, true, declarations)
}

func newAuthoredHouseChangeFixture(t *testing.T, goService, gateway bool, declarations string) (string, *compiler.Result) {
	t.Helper()
	root := t.TempDir()
	appSource := `application "change_test" {}
module "house" {
  source = "./house"
}
`
	packageSource := `package "house" {
}
`
	if goService {
		appSource = `workspace {
  managed_generated_roots = ["house/scenerycontract", "internal/scenerygen"]
}
go_module "application" {
  root        = "."
  import_path = "example.test/change-test"
}
application "change_test" {}
module "house" {
  source = "./house"
}
`
		packageSource = `package "house" {
  go_contract {
    import_path = "example.test/change-test/house"
  }
}
`
	}
	if gateway {
		appSource = `application "change_test" {}
http_gateway "public" {
  exposure        = "internet"
  base_path       = "/"
  cors            = std.cors.none
  trusted_proxies = std.trusted_proxies.none
  forwarded       = std.forwarded_headers.reject
}
module "house" {
  source = "./house"
  inputs = { gateway = http_gateway.public }
}
`
		packageSource = `package "house" {
}
input "gateway" {
  type = resource_ref("http_gateway")
}
`
	}
	writeNestedModuleFile(t, filepath.Join(root, testAppFilename), appSource)
	writeNestedModuleFile(t, filepath.Join(root, "house", testPackageFilename), packageSource+declarations)
	return root, compileChangeFixture(t, root)
}

func newHouseExecutionChangeFixture(t *testing.T) (string, *compiler.Result) {
	t.Helper()
	return newHouseChangeFixture(t, houseExecutionDeclarations)
}

func newHouseGoExecutionChangeFixture(t *testing.T) (string, *compiler.Result) {
	t.Helper()
	return newHouseGoChangeFixture(t, houseGoExecutionDeclarations)
}

func newHouseGatewayExecutionChangeFixture(t *testing.T) (string, *compiler.Result) {
	t.Helper()
	return newHouseGatewayChangeFixture(t, houseExecutionDeclarations)
}

const houseExecutionDeclarations = `service "house" {
  runtime = "test"
  implementation {
    constructor = "NewService"
  }
}
` + houseOperationDeclarations

const houseGoExecutionDeclarations = `service "house" {
  runtime = "go"
  implementation {
    constructor = "NewService"
  }
}
` + houseOperationDeclarations

const houseOperationDeclarations = `record "process_scene_input" {
  field "scene_id" {
    type = string
  }
}
record "process_scene_result" {
  field "status" {
    type = string
  }
}
operation "process_scene" {
  service = service.house
  input   = record.process_scene_input
  handler {
    method = "ProcessScene"
  }
  result "processed" {
    type = record.process_scene_result
  }
}
execution "process_scene_direct" {
  operation = operation.process_scene
  mode      = "direct"
  timeout   = "40m"
}
`

const testAuthenticationDeclaration = `authentication "test" {
  provider = std.provider.standard_auth
  scheme   = "session"
}
`

const testUnitExecutionDeclarations = `service "house" {
  runtime = "test"
  implementation {
    constructor = "NewService"
  }
}
operation "process_scene" {
  service = service.house
  input   = std.type.unit
  handler {
    method = "ProcessScene"
  }
}
execution "process_scene_direct" {
  operation = operation.process_scene
  mode      = "direct"
  timeout   = "40m"
}
`

func compileChangeFixture(t *testing.T, root string) *compiler.Result {
	t.Helper()
	result, err := compiler.Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile fixture: %v diagnostics=%#v", err, result.Diagnostics)
	}
	return result
}

func planChangesDryRunAndCaptureResult(t *testing.T, root string, request ChangeRequest) (ChangePlan, *compiler.Result) {
	t.Helper()
	checkPredicted := request.CheckPredictedGoContracts
	var predicted *compiler.Result
	request.CheckPredictedGoContracts = func(result *compiler.Result) error {
		predicted = result
		if checkPredicted != nil {
			return checkPredicted(result)
		}
		return nil
	}
	plan, err := PlanChangesDryRun(root, request)
	if err != nil {
		t.Fatal(err)
	}
	if predicted == nil {
		t.Fatal("predicted compiler result was not captured")
	}
	return plan, predicted
}

func plannedEditAfter(t *testing.T, plan ChangePlan, path string) []byte {
	t.Helper()
	path = filepath.ToSlash(path)
	for _, edit := range plan.Edits {
		if edit.Path == path {
			return edit.After
		}
	}
	t.Fatalf("plan does not edit %s: %#v", path, plan.Edits)
	return nil
}

func applyChangePlanAndCaptureResult(t *testing.T, root string, plan ChangePlan, base *compiler.Result) (ChangeReceipt, *compiler.Result) {
	t.Helper()
	var applied *compiler.Result
	receipt, err := ApplyChangePlanWithOptions(root, plan, ApplyOptions{
		ExpectedWorkspaceRevision: base.WorkspaceRevision,
		ExpectedContractRevision:  new(base.Manifest.ContractRevision),
		Caller:                    plan.Caller,
		GrantedCapabilities:       []string{requiredChangeCapability},
		CheckGenerated: func(result *compiler.Result) {
			applied = result
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied == nil {
		t.Fatal("applied compiler result was not captured")
	}
	return receipt, applied
}

func writeNestedModuleFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
