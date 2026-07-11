package bridge

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCommittedLegacyBridgeCallsFrozenHandler(t *testing.T) {
	raw, err := SceneryVNextBridgeEcho(context.Background(), []byte(`{"message":"roof"}`))
	if err != nil {
		t.Fatal(err)
	}
	var response EchoResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if response.Message != "legacy:roof" {
		t.Fatalf("message = %q", response.Message)
	}
}
