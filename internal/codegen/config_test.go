package codegen

import (
	"strings"
	"testing"

	"scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	generateapi "scenery.sh/internal/generate/api"
)

func TestWorkerEntrypointsRegisterOnlyTheirAdapter(t *testing.T) {
	plan := generateapi.RuntimeIntegrationPlan{ContractRevision: "sha256:contract", Services: []generateapi.ServiceProcessPlan{
		{Address: "echo/service/echo", Name: "echo_echo", AdapterImport: "example.test/app/internal/scenerygen/echo_echo_adapter", RequiredAddresses: []string{"echo/binding/echo_http", "echo/service/echo"}},
		{Address: "greeter/service/greeter", Name: "greeter_greeter", AdapterImport: "example.test/app/internal/scenerygen/greeter_greeter_adapter", RequiredAddresses: []string{"greeter/service/greeter"}},
	}}
	cfg := app.Config{Auth: app.AuthConfig{Enabled: true}}
	sql := compiler.SQLRequirements{{Kind: compiler.SQLStandardAuth, Name: "scenery", Schema: "scenery"}}
	output, err := Generate("app", cfg, plan, sql)
	if err != nil {
		t.Fatal(err)
	}
	if len(output.Generated) != 2 {
		t.Fatalf("generated %d entrypoints, want one per service", len(output.Generated))
	}
	echo := string(output.Generated[ProcessMainRoot+"/services/echo_echo/main.go"])
	for _, fragment := range []string{
		`scenerycomposition "example.test/app/internal/scenerygen/echo_echo_adapter"`,
		`sceneryauth "scenery.sh/auth"`, "sceneryauth.UseStandard()", `{Name: "scenery", Schema: "scenery"}`,
		`RequiredAddresses: []string{"echo/binding/echo_http", "echo/service/echo"}`,
		"scenerycomposition.Register(contractRegistry)", "contractRegistry.Seal()",
		`sceneryruntime.MainGoWorker(sceneryruntime.AppConfig{Name: "app"`, `"echo/service/echo")`,
	} {
		if !strings.Contains(echo, fragment) {
			t.Fatalf("worker entrypoint missing %q:\n%s", fragment, echo)
		}
	}
	if strings.Contains(echo, "greeter") {
		t.Fatalf("worker entrypoint links another registration:\n%s", echo)
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
	if output, err := Generate("frontend", app.Config{}, generateapi.RuntimeIntegrationPlan{ContractRevision: "sha256:contract"}, nil); err != nil || len(output.Generated) != 0 {
		t.Fatalf("an application without Go services rendered %d files, %v", len(output.Generated), err)
	}
}
