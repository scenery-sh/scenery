package eve

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/mcpcontract"
)

func TestScaffoldPackageFilesAreAuthoritativeAndDefensive(t *testing.T) {
	files, err := ScaffoldPackageFiles()
	if err != nil {
		t.Fatal(err)
	}
	lock := files["package-lock.json"]
	sum := sha256.Sum256(lock)
	if got, want := "sha256:"+hex.EncodeToString(sum[:]), "sha256:50688be5a4ea2b73acffd21b724caa699ea81e8343befd22b1212e89e845938a"; got != want {
		t.Fatalf("scaffold lock digest=%s want=%s", got, want)
	}
	var manifest struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(files["package.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Dependencies["eve"] != EveVersion {
		t.Fatalf("scaffold package eve=%q want=%q", manifest.Dependencies["eve"], EveVersion)
	}
	files["package-lock.json"][0] ^= 1
	again, err := ScaffoldPackageFiles()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(files["package-lock.json"], again["package-lock.json"]) {
		t.Fatal("scaffold package lock returned shared mutable bytes")
	}
}

func fixtureRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("testdata", "project"))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func testOverlayRequest(t *testing.T, source, destination string) OverlayRequest {
	t.Helper()
	return OverlayRequest{
		SourceRoot:         source,
		OverlayRoot:        destination,
		AssistantAddress:   "assistant/support",
		RuntimeRevision:    "sha256:runtime",
		CapabilityRevision: "sha256:capability",
		ControlURL:         "http://127.0.0.1:8123/scenery/v1/control",
		MCPURL:             "http://127.0.0.1:8124/mcp",
	}
}

func TestMaterializeOverlayCopiesAuthoredFilesAndReservesGeneratedPaths(t *testing.T) {
	source := fixtureRoot(t)
	before, err := os.ReadFile(filepath.Join(source, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(t.TempDir(), "overlay")
	request := testOverlayRequest(t, source, overlay)
	request.ApprovalNeverTools = []string{"scenery__safe", "scenery__safe"}
	result, err := MaterializeOverlay(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{
		"package.json",
		"package-lock.json",
		"agent/agent.ts",
		"agent/tools/local.ts",
		"agent/channels/scenery.ts",
		"agent/connections/scenery.ts",
		".scenery/bootstrap.mjs",
		".scenery/runtime-manifest.json",
	} {
		if _, err := os.Stat(filepath.Join(overlay, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("overlay missing %s: %v", relative, err)
		}
	}
	connection := string(mustRead(t, result.ConnectionPath))
	if !strings.Contains(connection, "SCENERY_MCP_URL") || strings.Contains(connection, "http://127.0.0.1:8124/mcp") {
		t.Fatalf("generated MCP connection did not use the runtime loopback seam: %s", connection)
	}
	for _, fragment := range []string{
		`const approvalNeverTools = new Set<string>(["scenery__safe"])`,
		`const principal = ctx.session.auth.current?.principalId?.trim();`,
		`if (!principal) throw new Error("Scenery authenticated principal is required");`,
		`does not expose a per-action call ID`,
		`Do not synthesize any of them`,
		`approvalNeverTools.has(toolName)`,
		`? "not-applicable"`,
		`: "user-approval"`,
	} {
		if !strings.Contains(connection, fragment) {
			t.Fatalf("generated MCP connection is missing approval policy %q: %s", fragment, connection)
		}
	}
	if strings.Contains(connection, "always()") {
		t.Fatalf("generated MCP connection still gates every capability: %s", connection)
	}
	for _, forbidden := range []string{"?? \"anonymous\"", "approvalToken", "approval_token", "SCENERY_APPROVAL", "request_id:", "trace_context:", "idempotency_key"} {
		if strings.Contains(connection, forbidden) {
			t.Fatalf("generated MCP connection exposed forbidden caller/approval material %q: %s", forbidden, connection)
		}
	}
	if strings.Contains(string(mustRead(t, result.ManifestPath)), "eve") {
		t.Fatalf("provider implementation leaked into runtime descriptor: %s", result.ManifestPath)
	}
	channel := string(mustRead(t, filepath.Join(overlay, "agent/channels/scenery.ts")))
	for _, fragment := range []string{
		`import { AsyncLocalStorage } from "node:async_hooks"`,
		`const approvalNeverTools = new Set<string>(["scenery__safe"])`,
		`requires_approval: !approvalNeverTools.has(action.toolName)`,
		"creatingConversationDigest.run(body.conversation_digest",
		"conversationDigests.get(sessionID) || creatingConversationDigest.getStore()",
		"from(continuationToken).send",
		"attachSession(body.private_session_id).send",
		"from(body.continuation_token).respond",
		`const optionId = body.decision === "allow" ? "approve" : "cancel"`,
		"attachSession(body.private_session_id).cancel()",
		"attachSession(sessionID)",
		"body.assistant_address !== assistantAddress",
		"body.runtime_revision !== runtimeRevision",
		"body.capability_revision !== capabilityRevision",
		`error: { code: "revision_mismatch", message: "assistant runtime revision mismatch" }`,
		"}), 409)",
		"function failureMessage(",
		`message: failureMessage(data)`,
	} {
		if !strings.Contains(channel, fragment) {
			t.Fatalf("generated private channel is missing revision guard %q: %s", fragment, channel)
		}
	}
	for _, forbidden := range []string{"args.send", "args.cancel", "args.getSession", "continuationToken:"} {
		if strings.Contains(channel, forbidden) {
			t.Fatalf("generated private channel still uses retired Eve channel API %q: %s", forbidden, channel)
		}
	}
	bootstrap := string(mustRead(t, result.BootstrapPath))
	for _, fragment := range []string{
		`const serverEntry = resolve(projectRoot, ".output/server/index.mjs")`,
		`process.env.NITRO_PORT = String(controlPort)`,
		`await import(pathToFileURL(serverEntry).href)`,
	} {
		if !strings.Contains(bootstrap, fragment) {
			t.Fatalf("generated bootstrap is missing direct server ownership %q: %s", fragment, bootstrap)
		}
	}
	if strings.Contains(bootstrap, "spawn(") || strings.Contains(bootstrap, "eve.js") {
		t.Fatalf("generated bootstrap reintroduced an unowned provider subprocess: %s", bootstrap)
	}
	lock := mustRead(t, filepath.Join(source, "package-lock.json"))
	digest := sha256.Sum256(lock)
	if want := "sha256:" + hex.EncodeToString(digest[:]); result.PackageLockDigest != want {
		t.Fatalf("lock digest=%q want=%q", result.PackageLockDigest, want)
	}
	after, err := os.ReadFile(filepath.Join(source, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("materialization changed authored package.json")
	}
	if _, err := MaterializeOverlay(testOverlayRequest(t, source, overlay)); err == nil {
		t.Fatal("existing overlay was silently overwritten")
	}
}

func TestApprovalNeverToolsQualifiesOnlyExplicitLocalExemptions(t *testing.T) {
	manifest := mcpcontract.Manifest{Capabilities: []mcpcontract.Capability{
		{Name: "write", Approval: mcpcontract.ApprovalAlways},
		{Name: "z_read", Approval: mcpcontract.ApprovalNever},
		{Name: "a_read", Approval: mcpcontract.ApprovalNever},
		{Name: "a_read", Approval: mcpcontract.ApprovalNever},
	}}
	got := ApprovalNeverTools(manifest)
	want := []string{"scenery__a_read", "scenery__z_read"}
	if len(got) != len(want) {
		t.Fatalf("approval-free tools = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("approval-free tools = %v, want %v", got, want)
		}
	}
}

func TestMaterializeOverlayRejectsReservedSourceClaimsAndUnsafeDestination(t *testing.T) {
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "agent/channels"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "agent/channels/scenery.ts"), []byte("authored"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MaterializeOverlay(testOverlayRequest(t, source, filepath.Join(t.TempDir(), "overlay"))); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("reserved path error=%v", err)
	}
	if _, err := MaterializeOverlay(testOverlayRequest(t, fixtureRoot(t), string(filepath.Separator))); err == nil {
		t.Fatal("filesystem root accepted as overlay")
	}
}

func TestReservedPathListIsDefensive(t *testing.T) {
	paths := ReservedPathList()
	paths[0] = "changed"
	again := ReservedPathList()
	if again[0] == "changed" {
		t.Fatal("reserved path list is mutable")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}
