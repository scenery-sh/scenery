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
	if strings.Contains(host, "scenerygen") || strings.Contains(host, "sceneryruntime.Main(") {
		t.Fatalf("host entrypoint links service registrations:\n%s", host)
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
