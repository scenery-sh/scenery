package native

import (
	"testing"

	contract "example.test/polyglot/native/scenerycontract"
)

func TestLabelUsesGeneratedOutcome(t *testing.T) {
	service, err := NewService(t.Context(), contract.NativeConstructorInput{})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := service.Label(t.Context(), contract.LabelInput{})
	if label, ok := outcome.(contract.LabelOk); err != nil || !ok || label.Value.Vendor != "native" {
		t.Fatalf("Label() = %#v, %v", outcome, err)
	}
}
