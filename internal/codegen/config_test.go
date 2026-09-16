package codegen

import (
	"strings"
	"testing"

	"scenery.sh/internal/app"
	generateapi "scenery.sh/internal/generate/api"
)

func TestGeneratedPreflightPrecedesApplicationInitialization(t *testing.T) {
	generated, err := generateMain("fixture", app.Config{Auth: app.AuthConfig{Enabled: true}}, "example.test/fixture/internal/composition", nil)
	if err != nil {
		t.Fatal(err)
	}
	source := string(generated)
	if !strings.Contains(source, `Name: "fixture"`) {
		t.Fatal("entrypoint lost the explicitly supplied application name")
	}
	preflight := strings.Index(source, "sceneryruntime.WriteRuntimePreflight(")
	if preflight < 0 {
		t.Fatal("missing read-only runtime handshake")
	}
	for _, call := range []string{"sceneryauth.RegisterStandard(", "scenerycomposition.Register(", "sceneryruntime.Main("} {
		if position := strings.Index(source, call); position < preflight {
			t.Fatalf("%s runs before preflight or is missing", call)
		}
	}
	if !strings.Contains(source[:strings.Index(source, "sceneryauth.RegisterStandard(")], "return\n") {
		t.Fatal("preflight falls through to application initialization")
	}
}

func TestServiceProcessEntrypointsRegisterOnlyTheirAdapter(t *testing.T) {
	const composition = "example.test/app/internal/scenerygen/composition"
	plan := generateapi.RuntimeIntegrationPlan{CompositionImport: composition, ContractRevision: "sha256:contract", Services: []generateapi.ServiceProcessPlan{
		{Address: "echo/service/echo", Name: "echo_echo", AdapterImport: "example.test/app/internal/scenerygen/echo_echo_adapter", RequiredAddresses: []string{"echo/binding/echo_http", "echo/service/echo"},
			Routes: []generateapi.ServiceProcessRoute{{Methods: []string{"POST"}, Path: "/echo"}}},
		{Address: "greeter/service/greeter", Name: "greeter_greeter", AdapterImport: "example.test/app/internal/scenerygen/greeter_greeter_adapter", RequiredAddresses: []string{"greeter/service/greeter"},
			Routes: []generateapi.ServiceProcessRoute{{Methods: []string{"GET"}, Path: "/files/*path", PathTail: true}}},
	}}
	output, err := Generate("app", app.Config{Auth: app.AuthConfig{Enabled: true}}, plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	echo := string(output.Generated[ProcessMainRoot+"/services/echo_echo/main.go"])
	for _, fragment := range []string{
		`scenerycomposition "example.test/app/internal/scenerygen/echo_echo_adapter"`,
		`RequiredAddresses: []string{"echo/binding/echo_http", "echo/service/echo"}`,
		"sceneryruntime.WriteRuntimePreflight(proof, scenerycomposition.ContractRevision)",
		"sceneryauth.RegisterStandard(", "scenerycomposition.Register(contractRegistry)", "contractRegistry.Seal()", "sceneryruntime.Main(",
	} {
		if !strings.Contains(echo, fragment) {
			t.Fatalf("service entrypoint missing %q:\n%s", fragment, echo)
		}
	}
	if strings.Contains(echo, composition) || strings.Contains(echo, "greeter") {
		t.Fatalf("service entrypoint links another registration:\n%s", echo)
	}
	if main := string(output.Generated["scenery_internal_main/main.go"]); !strings.Contains(main, "RequiredAddresses: scenerycomposition.RequiredAddresses") || !strings.Contains(main, composition) {
		t.Fatalf("application entrypoint changed:\n%s", main)
	}
	host := string(output.Generated[ProcessMainRoot+"/host/main.go"])
	for _, fragment := range []string{
		`const contractRevision = "sha256:contract"`, "sceneryruntime.WriteRuntimePreflight(proof, contractRevision)", "sceneryruntime.VerifyLinkedContractBundle(contractRevision)",
		`Fallback: "echo_echo"`, `{Process: "echo_echo", Methods: []string{"POST"}, Path: "/echo", PathTail: false}`,
		`{Process: "greeter_greeter", Methods: []string{"GET"}, Path: "/files/*path", PathTail: true}`,
	} {
		if !strings.Contains(host, fragment) {
			t.Fatalf("host entrypoint missing %q:\n%s", fragment, host)
		}
	}
	if strings.Contains(host, "scenerygen") || strings.Contains(host, "sceneryruntime.Main(") || strings.Contains(host, "registerApplication") || strings.Contains(host, "sceneryauth") {
		t.Fatalf("host entrypoint links service or application registrations:\n%s", host)
	}
	if _, exists := output.Generated[ProcessMainRoot+"/host/application.go"]; exists {
		t.Fatal("host without application registrations rendered application.go")
	}
	withApplication := plan
	withApplication.HostApplication = []byte("package main\n")
	withApplication.Services = append([]generateapi.ServiceProcessPlan(nil), plan.Services...)
	withApplication.Services[0].MCPTools = []generateapi.ServiceProcessMCPTool{{AssistantAddress: "app/assistant/support", Name: "echo__echo"}}
	output, err = Generate("app", app.Config{Auth: app.AuthConfig{Enabled: true}}, withApplication, nil)
	if err != nil {
		t.Fatal(err)
	}
	host = string(output.Generated[ProcessMainRoot+"/host/main.go"])
	for _, fragment := range []string{
		"sceneryauth.RegisterStandard(", "RequiredAddresses: applicationRequiredAddresses", "registerApplication(contractRegistry)", "contractRegistry.Seal()",
		`{Process: "echo_echo", AssistantAddress: "app/assistant/support", Name: "echo__echo"}`,
	} {
		if !strings.Contains(host, fragment) {
			t.Fatalf("application host entrypoint missing %q:\n%s", fragment, host)
		}
	}
	if string(output.Generated[ProcessMainRoot+"/host/application.go"]) != "package main\n" {
		t.Fatal("host application registrations were not rendered beside the entrypoint")
	}
	for name, invalid := range map[string]generateapi.ServiceProcessPlan{
		"duplicate": plan.Services[0],
		"unscoped":  {Address: "x/service/x", Name: "x", AdapterImport: "example.test/x"},
	} {
		broken := plan
		broken.Services = append(append([]generateapi.ServiceProcessPlan(nil), plan.Services...), invalid)
		if _, err := Generate("app", app.Config{}, broken, nil); err == nil {
			t.Errorf("%s service process plan was accepted", name)
		}
	}
}

// An application without a native service still runs the process model: a
// host alone, which verifies the linked contract and serves framework routes
// itself.
func TestApplicationWithoutServicesRendersAHostServingItself(t *testing.T) {
	output, err := Generate("frontend", app.Config{}, generateapi.RuntimeIntegrationPlan{ContractRevision: "sha256:contract"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	host := string(output.Generated[ProcessMainRoot+"/host/main.go"])
	for _, fragment := range []string{`const contractRevision = "sha256:contract"`, `Fallback: ""`, "sceneryruntime.MainProcessHost("} {
		if !strings.Contains(host, fragment) {
			t.Fatalf("host entrypoint without services missing %q:\n%s", fragment, host)
		}
	}
	for path := range output.Generated {
		if strings.HasPrefix(path, ProcessMainRoot+"/services/") {
			t.Fatalf("an application without services rendered %s", path)
		}
	}
	if output, err := Generate("frontend", app.Config{}, generateapi.RuntimeIntegrationPlan{}, nil); err != nil || len(output.Generated) != 1 {
		t.Fatalf("a plan without a contract rendered %d files, %v", len(output.Generated), err)
	}
}
