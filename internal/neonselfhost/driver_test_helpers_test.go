package neonselfhost

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func closedLoopbackPortForDriverTest(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	return port
}

func startFakePageserver(t *testing.T) (*http.Server, int, func() []string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var mu sync.Mutex
	var requests []string
	timelines := map[string]bool{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		mu.Lock()
		requests = append(requests, strings.TrimSpace(r.Method+" "+r.URL.RequestURI()+" "+string(body)))
		mu.Unlock()
		if r.Method == http.MethodGet {
			if strings.HasSuffix(r.URL.Path, "/get_lsn_by_timestamp") {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"lsn":"0/500"}`))
				return
			}
			parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
			if len(parts) >= 5 && timelines[parts[4]] {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"timeline_id":` + strconv.Quote(parts[4]) + `}`))
				return
			}
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/timeline") {
			var payload struct {
				NewTimelineID string `json:"new_timeline_id"`
			}
			if err := json.Unmarshal(body, &payload); err == nil && strings.TrimSpace(payload.NewTimelineID) != "" {
				mu.Lock()
				timelines[payload.NewTimelineID] = true
				mu.Unlock()
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	server := &http.Server{Handler: handler}
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		_ = server.Close()
	})
	return server, listener.Addr().(*net.TCPAddr).Port, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), requests...)
	}
}

func writeCellStateForTest(t *testing.T, root string, pageserverPort int) {
	t.Helper()
	data := []byte(fmt.Sprintf(`{"root":%q,"ports":{"pageserver_http":%d}}`, root, pageserverPort))
	if err := os.WriteFile(filepath.Join(root, "cell.json"), data, 0o644); err != nil {
		t.Fatalf("write cell: %v", err)
	}
}

func writeComputeTemplatesForTest(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, "compute_templates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "compute.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFakePSQL(t *testing.T, bin string, logPath string, databaseState string) {
	t.Helper()
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	psql := filepath.Join(bin, "psql")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$PSQL_LOG"
case "$*" in
  *"select 1 from pg_database"*)
    if [ "$DATABASE_STATE" = "exists" ]; then
      printf '1\n'
    fi
    exit 0
    ;;
  *"create database"*)
    exit 0
    ;;
  *"select 1"*)
    printf '1\n'
    exit 0
    ;;
esac
echo "unexpected psql $*" >&2
exit 1
`
	if err := os.WriteFile(psql, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PSQL_LOG", logPath)
	t.Setenv("DATABASE_STATE", databaseState)
}
