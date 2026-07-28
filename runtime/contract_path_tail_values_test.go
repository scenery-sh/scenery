package runtime

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"scenery.sh/internal/contract"
)

func TestPathTailConstructsCanonicalRelativePathValues(t *testing.T) {
	type requiredInput struct {
		Path contract.RelativePath `json:"path"`
	}
	type optionalInput struct {
		Path contract.Optional[contract.RelativePath] `json:"path"`
	}
	request := httptest.NewRequest(http.MethodGet, "/drive", nil)
	requiredSchema := ContractRequestSchema{Mappings: []ContractInputMapping{{
		Source: ContractSourcePathTail, Name: "path", Target: "path", Type: "relative_path",
	}}}
	decoded, err := DecodeContractInput[requiredInput](request, map[string]string{"path": "models/Cafe%CC%81/file"}, requiredSchema)
	if err != nil || decoded.Path != contract.RelativePath("models/Café/file") {
		t.Fatalf("required relative path = %q, %v", decoded.Path, err)
	}
	if _, err := DecodeContractInput[requiredInput](request, map[string]string{"path": ""}, requiredSchema); err == nil {
		t.Fatal("empty required relative path tail was accepted")
	}

	optionalSchema := ContractRequestSchema{Mappings: []ContractInputMapping{{
		Source: ContractSourcePathTail, Name: "path", Target: "path", Type: "optional(relative_path)", Optional: true,
	}}}
	absent, err := DecodeContractInput[optionalInput](request, map[string]string{"path": ""}, optionalSchema)
	if err != nil || absent.Path.Set {
		t.Fatalf("empty optional relative path = %#v, %v", absent.Path, err)
	}
	present, err := DecodeContractInput[optionalInput](request, map[string]string{"path": "assets/logo.svg"}, optionalSchema)
	if err != nil || !present.Path.Set || present.Path.Value != contract.RelativePath("assets/logo.svg") {
		t.Fatalf("present optional relative path = %#v, %v", present.Path, err)
	}
}
