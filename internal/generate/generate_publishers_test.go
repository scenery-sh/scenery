package generate

import (
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
)

func TestGeneratePolyglotEventPublishers(t *testing.T) {
	result, err := compiler.Compile("../../testdata/apps/polyglot")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid() {
		t.Fatalf("fixture: %+v", result.Diagnostics)
	}
	builders := []struct {
		build func() ([]generatedFile, error)
		want  map[string][]string
	}{
		{func() ([]generatedFile, error) { return renderGoPackages(result) }, map[string][]string{
			"consumer/scenerycontract/contract.gen.go": {"type AcceptedEventPublisher interface", "Publishers ConsumerPublishers", "type AcceptedEventPayload = NativeClientEventPayload"},
			"consumer/scenerycontract/types.gen.go":    {"type NativeClientEventPayload struct"},
		}},
		{func() ([]generatedFile, error) { return renderRustContractFiles(result, rustSourceModules(result)) }, map[string][]string{
			"pricing/scenerycontract/src/lib.rs": {"pub struct NativeDeliveredOrder", "pub publishers: PricingPublishers", `ctx.emit_event("marker", &payload).await`},
		}},
		{func() ([]generatedFile, error) { return renderJSContractFiles(result, jsServices(result)) }, map[string][]string{
			"typescript/scenerycontract/types.ts": {"export interface NativeDeliveredOrder", "MarkerEventPayload = NativeDeliveredOrder"},
			"typescript/scenerycontract/index.ts": {"export const publishers = Object.freeze", "readonly publishers: typeof publishers", `ctx.eventEmit("marker", wire)`},
			"typescript/scenerycontract/main.ts":  {"publishers: Contract.publishers"},
		}},
	}
	for _, builder := range builders {
		files, err := builder.build()
		if err != nil {
			t.Fatal(err)
		}
		contents := make(map[string]string, len(files))
		for _, file := range files {
			relative, err := filepath.Rel(result.Root, file.Path)
			if err != nil {
				t.Fatal(err)
			}
			contents[filepath.ToSlash(relative)] = string(file.Bytes)
		}
		for file, fragments := range builder.want {
			for _, fragment := range fragments {
				if !strings.Contains(strings.Join(strings.Fields(contents[file]), " "), fragment) {
					t.Errorf("%s missing %q", file, fragment)
				}
			}
		}
	}
}
