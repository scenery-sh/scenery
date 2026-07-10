package vnext

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateContractsAndTypeScriptAreStable(t *testing.T) {
	temp := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), temp)
	_ = os.RemoveAll(filepath.Join(temp, "house", "scenerycontract"))
	_ = os.RemoveAll(filepath.Join(temp, "clients"))
	goResult, err := GenerateGoContracts(temp, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(goResult.Changed) != 3 {
		t.Fatalf("Go changed = %#v", goResult.Changed)
	}
	if _, err := GenerateGoContracts(temp, true); err != nil {
		t.Fatal(err)
	}
	tsResult, err := GenerateTypeScriptClients(temp, "public_api", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(tsResult.Changed) != 7 {
		t.Fatalf("TS changed = %#v", tsResult.Changed)
	}
	if _, err := GenerateTypeScriptClients(temp, "public_api", true); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"types.ts", "runtime.ts", "client.ts", "metadata.ts", "index.ts", "scenery.client-selection.v1.json", "scenery.typescript-client-generated.v1.json"} {
		if _, err := os.Stat(filepath.Join(temp, "clients", "generated", "public_api", path)); err != nil {
			t.Error(err)
		}
	}
}
