package build

import (
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/codegen"
	"scenery.sh/internal/model"
)

func TestGenerateNativeContractApplicationEntrypointInProcess(t *testing.T) {
	t.Parallel()

	const compositionImport = "example.test/nativeapp/internal/scenerygen/composition"
	generated, err := codegen.Generate(
		&model.App{Name: "nativeapp"},
		appcfg.Config{Name: "nativeapp"},
		compositionImport,
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
}
