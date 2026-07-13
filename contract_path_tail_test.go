package scenery

import (
	"net/http"
	"net/http/httptest"
	"testing"

	sceneryruntime "scenery.sh/runtime"
)

func TestPathTailConstructsCanonicalRelativePathValues(t *testing.T) {
	type requiredInput struct {
		Path RelativePath `json:"path"`
	}
	type optionalInput struct {
		Path Optional[RelativePath] `json:"path"`
	}
	request := httptest.NewRequest(http.MethodGet, "/drive", nil)
	requiredSchema := sceneryruntime.ContractRequestSchema{Mappings: []sceneryruntime.ContractInputMapping{{
		Source: sceneryruntime.ContractSourcePathTail, Name: "path", Target: "path", Type: "relative_path",
	}}}
	decoded, err := sceneryruntime.DecodeContractInput[requiredInput](request, map[string]string{"path": "models/Cafe%CC%81/file"}, requiredSchema)
	if err != nil || decoded.Path != RelativePath("models/Café/file") {
		t.Fatalf("required relative path = %q, %v", decoded.Path, err)
	}
	if _, err := sceneryruntime.DecodeContractInput[requiredInput](request, map[string]string{"path": ""}, requiredSchema); err == nil {
		t.Fatal("empty required relative path tail was accepted")
	}

	optionalSchema := sceneryruntime.ContractRequestSchema{Mappings: []sceneryruntime.ContractInputMapping{{
		Source: sceneryruntime.ContractSourcePathTail, Name: "path", Target: "path", Type: "optional(relative_path)", Optional: true,
	}}}
	absent, err := sceneryruntime.DecodeContractInput[optionalInput](request, map[string]string{"path": ""}, optionalSchema)
	if err != nil || absent.Path.Set {
		t.Fatalf("empty optional relative path = %#v, %v", absent.Path, err)
	}
	present, err := sceneryruntime.DecodeContractInput[optionalInput](request, map[string]string{"path": "assets/logo.svg"}, optionalSchema)
	if err != nil || !present.Path.Set || present.Path.Value != RelativePath("assets/logo.svg") {
		t.Fatalf("present optional relative path = %#v, %v", present.Path, err)
	}
}
