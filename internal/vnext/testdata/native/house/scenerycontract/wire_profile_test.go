package scenerycontract

import (
	"encoding/json"
	"testing"

	scenery "scenery.sh"
)

func TestGeneratedWireProfileRoundTripsExactValues(t *testing.T) {
	value := WireProfile{
		Count: 10,
		Mode:  ProcessModeRoofOnly,
		State: ProcessStateReady{Value: ReadyState{Message: "ready"}},
		Tags:  scenery.Set[string]{"z", "a"},
		UnknownFields: map[string]scenery.JSON{
			"future": json.RawMessage(`{"large":9007199254740993}`),
		},
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"count":"10","future":{"large":9007199254740993},"mode":"roof-only","state":{"kind":"ready","message":"ready"},"tags":["a","z"]}`
	if string(encoded) != want {
		t.Fatalf("encoded = %s, want %s", encoded, want)
	}
	var decoded WireProfile
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Count != 10 || decoded.Mode != ProcessModeRoofOnly {
		t.Fatalf("decoded = %#v", decoded)
	}
	ready, ok := decoded.State.(ProcessStateReady)
	if !ok || ready.Value.Message != "ready" {
		t.Fatalf("state = %#v", decoded.State)
	}
}

func TestGeneratedWireProfileRejectsInvalidClosedValuesAndConstraints(t *testing.T) {
	invalid := WireProfile{Count: 101, Mode: ProcessMode("future"), State: ProcessStateReady{Value: ReadyState{Message: ""}}}
	if _, err := json.Marshal(invalid); err == nil {
		t.Fatal("invalid generated value was accepted")
	}
	var decoded WireProfile
	if err := json.Unmarshal([]byte(`{"count":10,"mode":"all","state":{"kind":"future","x":1},"tags":[]}`), &decoded); err == nil {
		t.Fatal("numeric int64 wire form was accepted")
	}
}
