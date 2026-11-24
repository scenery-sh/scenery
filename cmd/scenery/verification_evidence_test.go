package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/harnessevidence"
	"scenery.sh/internal/validation"
)

func TestJUnitReconcilesBunAndNodeReports(t *testing.T) {
	t.Parallel()
	cases := []struct {
		data      string
		complete  bool
		terminals int
	}{
		{`<testsuites tests="2"><testsuite name="file.test.ts" tests="2"><testcase name="one" time="0.01"/><testcase name="later" time="0"><skipped message="TODO"/></testcase></testsuite></testsuites>`, true, 2},
		{`<testsuites><testcase file="file.test.ts" name="one" time="0.03"/><!-- tests 1 --></testsuites>`, true, 1},
		{`<testsuites tests="2"><testsuite tests="2" name="file.test.ts"><testcase name="one" time="0.01"/></testsuite></testsuites>`, false, 1},
		{`<testsuites><testcase file="file.test.ts" name="one" time="NaN"/><!-- tests 1 --></testsuites>`, false, 1},
		{`<testsuites><testcase file="file.test.ts" name="one" time="0"/><testcase file="file.test.ts" name="one" time="0"/><!-- tests 2 --></testsuites>`, false, 2},
		{`<testsuites><testcase file="other.test.ts" name="one" time="0"/><!-- tests 1 --></testsuites>`, false, 1},
		{`<testsuites><testcase`, false, 0},
	}
	for _, entry := range cases {
		result := harnessevidence.ParseJUnit([]byte(entry.data), []string{"file.test.ts"}, 1)
		if result.Completeness.Complete != entry.complete || result.Completeness.Terminal != entry.terminals {
			t.Fatalf("JUnit completeness=%+v cases=%+v", result.Completeness, result.Cases)
		}
	}
	replay := harnessevidence.ParseJUnit([]byte(cases[1].data), []string{"file.test.ts"}, 2)
	if replay.Cases[0].FirstAttemptOutcome != "incomplete" {
		t.Fatal("unknown first attempt was manufactured")
	}
	node := harnessevidence.ParseJUnit([]byte(`<testsuites>
<testcase name="todo" time="0"><skipped type="todo" message="true"/></testcase>
<testcase name="skipped" time="0"><skipped type="skipped" message="TODO reason"/></testcase>
<testcase name="timeout" time="0.005"><failure type="testTimeoutFailure"/></testcase>
<testcase name="bun-timeout" time="0.005"><failure type="TimeoutError"/></testcase>
<testcase name="canceled" time="0.001"><failure type="cancelledByParent"/></testcase>
<!-- tests 5 --></testsuites>`), []string{"file.test.ts"}, 1)
	if !node.Completeness.Complete || node.Completeness.Executed != 3 || node.Cases[0].Outcome != "todo" || node.Cases[1].Outcome != "skipped" || node.Cases[2].Outcome != "timed_out" || node.Cases[3].Outcome != "timed_out" || node.Cases[4].Outcome != "canceled" {
		t.Fatalf("Node status attributes lost: %+v", node)
	}
}

func TestValidationArchiveKeepsPlanAndHistoryBeforeLatest(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	result := validationResultResponse{cliPayloadIdentity: newCLIPayloadIdentity(validationResultKind), Run: validationRunIdentity{ID: "one"}, Profile: "quick", Plan: validationPlanResponse{RunID: "one", Steps: []validation.PlanStep{{ID: "check", Command: []string{"tool", "check"}}}}, Outcome: "passed"}
	if err := writeValidationResult(root, &result); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(result.Wrote)
	if err != nil {
		t.Fatal(err)
	}
	result.Run.ID = "two"
	result.Plan.RunID = "two"
	result.Outcome = "failed"
	if err := writeValidationResult(root, &result); err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(filepath.Join(root, ".scenery/harness/validation/runs/one/result.json"))
	if err != nil || !bytes.Equal(old, first) {
		t.Fatal("historical result was overwritten")
	}
	plan, err := os.ReadFile(filepath.Join(root, ".scenery/harness/validation/runs/one/plan.json"))
	if err != nil || !bytes.Contains(plan, []byte(`"tool"`)) {
		t.Fatal("resolved command plan was lost")
	}
	if err := writeValidationResult(root, &result); err == nil {
		t.Fatal("run overwrite accepted")
	}
}

type readinessDoer func(*http.Request) (*http.Response, error)

func (do readinessDoer) Do(req *http.Request) (*http.Response, error) { return do(req) }

func TestFrontendReadinessRequiresAdvertisedModules(t *testing.T) {
	t.Parallel()
	missing := true
	client := readinessDoer(func(req *http.Request) (*http.Response, error) {
		status := 200
		body := []byte(`<script type="module" src="/client.js"></script>`)
		if req.URL.Path == "/client.js" {
			body = []byte("export const ready=true")
			if missing {
				status = 503
			}
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})
	if _, err := probeFrontendHTTP(context.Background(), "127.0.0.1:1234", client); err == nil {
		t.Fatal("listener/HTML hid a missing client module")
	}
	missing = false
	count, err := probeFrontendHTTP(context.Background(), "127.0.0.1:1234", client)
	if err != nil || count != 1 {
		t.Fatalf("modules=%d err=%v", count, err)
	}
}

func TestFrontendReadinessRejectsHTMLModuleAndUnresolvedRedirect(t *testing.T) {
	t.Parallel()
	for _, status := range []int{200, 302} {
		client := readinessDoer(func(req *http.Request) (*http.Response, error) {
			header := http.Header{"Content-Type": []string{"text/html"}}
			body := `<script type="module" src="/client.js"></script>`
			if req.URL.Path != "/client.js" {
				return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
			}
			return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		if _, err := probeFrontendHTTP(context.Background(), "127.0.0.1:1234", client); err == nil {
			t.Fatalf("module HTTP %d with an HTML fallback/unresolved redirect was accepted", status)
		}
	}
}

func TestValidationSourceRevisionTracksChangesWithoutManagedCaches(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "app.scn")
	if err := os.WriteFile(path, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := validationInputRevision(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "node_modules/cache"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}
	same, _ := validationInputRevision(root)
	if first != same {
		t.Fatal("dependency cache changed source identity")
	}
	if err := os.WriteFile(path, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, _ := validationInputRevision(root)
	if changed == first {
		t.Fatal("source edit did not change identity")
	}
}
