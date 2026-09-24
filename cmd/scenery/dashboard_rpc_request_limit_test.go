package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"scenery.sh/internal/devdash"
)

func TestRuntimeRPCAnswersARequestAtTheLimitAndClosesBeyondIt(t *testing.T) {
	t.Parallel()

	server := newDashboardServerWithController(runtimeRPCTestController{status: devdash.AppStatus{AppID: "session-a", Running: true}}, t.TempDir(), "127.0.0.1:0", nil)
	backend := httptest.NewServer(http.HandlerFunc(server.handleWebSocket))
	t.Cleanup(backend.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(backend.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}

	// Whitespace pads a status request to exactly the limit; it is answered.
	const status = `{"jsonrpc":"2.0","id":1,"method":"status"}`
	request := status[:len(status)-1] + strings.Repeat(" ", runtimeRPCMaxRequestBytes-len(status)) + "}"
	if err := conn.WriteMessage(websocket.TextMessage, []byte(request)); err != nil {
		t.Fatal(err)
	}
	var answer struct {
		ID     int             `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if err := conn.ReadJSON(&answer); err != nil || answer.ID != 1 || answer.Error != nil || !strings.Contains(string(answer.Result), `"app_id":"session-a"`) {
		t.Fatalf("request of %d bytes: id %d, result %s, error %+v, read error %v", len(request), answer.ID, answer.Result, answer.Error, err)
	}

	// A frame header that announces one byte more closes the connection with
	// 1009 at once: the runtime does not wait for, or read, its payload.
	header := make([]byte, 14)
	header[0] = 0x81       // FIN, text
	header[1] = 0x80 | 127 // masked, 64-bit payload length; the mask key stays zero
	binary.BigEndian.PutUint64(header[2:10], runtimeRPCMaxRequestBytes+1)
	if _, err := conn.NetConn().Write(header); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.ReadMessage(); !websocket.IsCloseError(err, websocket.CloseMessageTooBig) {
		t.Fatalf("read after an oversized request = %v, want close 1009 (message too big)", err)
	}
}

// The generated client refuses exactly the calls the runtime would close the
// connection for.
func TestRuntimeRPCRequestLimitIsTheGeneratedClientLimit(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile(filepath.Join("..", "..", "internal", "generate", "dev_runtime_client.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("export const DEV_RUNTIME_MAX_REQUEST_BYTES = %d;", runtimeRPCMaxRequestBytes); !strings.Contains(string(source), want) {
		t.Fatalf("dev_runtime_client.ts does not declare %q", want)
	}
}
