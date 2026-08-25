package tscheck

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassifyGeneratedAndApplicationDiagnosticsInProcess(t *testing.T) {
	t.Parallel()

	generated := classifyCheckFailure([]byte("/tmp/.scenery-tscheck-test/react/orders.generated.tsx(1,1): error TS2322"), errors.New("exit status 1"))
	if generated.Code != "SCN6320" || generated.Classification != "incompatible declared override" {
		t.Fatalf("generated diagnostic = %#v", generated)
	}
	application := classifyCheckFailure([]byte("/app/components/broken.tsx(1,1): error TS2304"), errors.New("exit status 1"))
	if application.Code != "SCN6321" || application.Classification != "unrelated application error" {
		t.Fatalf("application diagnostic = %#v", application)
	}
	withoutOutput := classifyCheckFailure(nil, errors.New("signal: killed"))
	if withoutOutput.Output != "signal: killed" {
		t.Fatalf("empty-output diagnostic = %#v", withoutOutput)
	}
}

func TestCheckRequiresNodeModulesBeforeStartingChecker(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Check(context.Background(), "/missing", root, filepath.Join(root, "generated"), "tsconfig.json", nil)
	classified, ok := err.(*Error)
	if !ok || classified.Code != "SCN6322" {
		t.Fatalf("readiness diagnostic = %#v", err)
	}
}

func TestStagedUIPathsRedirectSceneryAliasesInProcess(t *testing.T) {
	t.Parallel()

	configPath := filepath.FromSlash("/repo/app/tsconfig.json")
	stageRoot := filepath.FromSlash("/repo/app/src/generated/.scenery-tscheck-stage")
	paths, err := stagedUIPaths([]byte(`{"compilerOptions":{"paths":{"@/*":["./src/*"],"@scenery/ui":["./old/index.ts"]}}}`), configPath, stageRoot)
	if err != nil {
		t.Fatal(err)
	}
	if got := paths["@scenery/ui"]; len(got) != 1 || got[0] != filepath.ToSlash(filepath.Join(stageRoot, "react", "scenery-ui", "index.ts")) || strings.Contains(got[0], "/old/") {
		t.Fatalf("@scenery/ui paths = %#v", got)
	}
	if got := paths["@scenery/ui/tokens.stylex"]; len(got) != 1 || got[0] != filepath.ToSlash(filepath.Join(stageRoot, "react", "scenery-ui", "tokens.stylex.ts")) {
		t.Fatalf("@scenery/ui/tokens.stylex paths = %#v", got)
	}
	if got := paths["@/*"]; len(got) != 1 || got[0] != filepath.ToSlash(filepath.Join("/repo/app", "src", "*")) {
		t.Fatalf("@/* paths = %#v", got)
	}
}
