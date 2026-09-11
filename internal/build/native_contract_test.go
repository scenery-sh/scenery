package build

import (
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/codegen"
	"scenery.sh/internal/compiler"
)

func TestGenerateNativeContractApplicationEntrypointInProcess(t *testing.T) {
	t.Parallel()

	const compositionImport = "example.test/nativeapp/internal/scenerygen/composition"
	generated, err := codegen.Generate(
		"nativeapp",
		appcfg.Config{Name: "nativeapp"},
		compositionImport,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	mainSource := string(generated.Generated["scenery_internal_main/main.go"])
	for _, fragment := range []string{
		`scenerycomposition "` + compositionImport + `"`,
		"sceneryruntime.VerifyLinkedContractBundle(scenerycomposition.ContractRevision)",
		"sceneryruntime.NewContractRegistry",
		"scenerycomposition.Register(contractRegistry)",
		"contractRegistry.Seal()",
	} {
		if !strings.Contains(mainSource, fragment) {
			t.Fatalf("generated main missing %q:\n%s", fragment, mainSource)
		}
	}
	if strings.Contains(mainSource, "ConfigureSQLBindings") {
		t.Fatal("no-SQL entrypoint must not configure SQL supply")
	}
	for _, authEnabled := range []bool{false, true} {
		requirements := compiler.SQLRequirements{
			{Kind: compiler.SQLDataSource, Name: "billing-data", Schema: "billing_data"},
			{Kind: compiler.SQLDurable, Name: "scenery", Schema: "scenery"},
		}
		if authEnabled {
			requirements = append(requirements, compiler.SQLRequirement{Kind: compiler.SQLStandardAuth, Name: "scenery", Schema: "scenery"})
		}
		generated, err := codegen.Generate("nativeapp", appcfg.Config{Name: "nativeapp", Auth: appcfg.AuthConfig{Enabled: authEnabled}}, compositionImport, requirements)
		if err != nil {
			t.Fatal(err)
		}
		source := string(generated.Generated["scenery_internal_main/main.go"])
		if strings.Count(source, `Name: "scenery"`) != 1 || !strings.Contains(source, `Name: "billing-data", Schema: "billing_data", DurableOnly: false`) {
			t.Fatalf("binding deduplication or schema changed:\n%s", source)
		}
		want := `Name: "scenery", Schema: "scenery", DurableOnly: true`
		if authEnabled {
			want = `Name: "scenery", Schema: "scenery", DurableOnly: false`
		}
		if !strings.Contains(source, want) || strings.Index(source, "ConfigureSQLBindings") > strings.Index(source, "scenerycomposition.Register") {
			t.Fatalf("compiled SQL bindings must precede constructors:\n%s", source)
		}
		if authEnabled && strings.Index(source, "ConfigureSQLBindings") > strings.Index(source, "sceneryauth.RegisterStandard") {
			t.Fatal("auth registration preceded SQL bindings")
		}
	}
}
