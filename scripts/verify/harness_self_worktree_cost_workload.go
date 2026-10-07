package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
)

// Add real managed frontend and Eve helper processes only to the explicitly
// selected cost workload. Functional SQL ownership keeps its small fixture.
func (p *worktreeRuntimeProbe) prepareCostWorkload(root string) error {
	path := filepath.Join(root, ".scenery.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		return err
	}
	config["root"] = "web"
	config["frontends"] = map[string]any{"web": map[string]string{"root": "web"}}
	data, err = json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	files := map[string][]byte{
		".scenery.json":    data,
		"web/package.json": []byte(`{"name":"scenery-cost-frontend","private":true,"packageManager":"bun@1.3.14","scripts":{"dev":"bun server.ts"}}`),
		"web/server.ts": []byte(`const port = Number(Bun.argv[Bun.argv.indexOf("--port") + 1]);
Bun.serve({hostname:"127.0.0.1",port,fetch:request => new URL(request.url).pathname === "/client.js"
  ? new Response("document.body.dataset.generation = 'cost-workload';", {headers:{"Content-Type":"text/javascript"}})
  : new Response('<h1>Scenery resource workload</h1><script type="module" src="/client.js"></script>', {headers:{"Content-Type":"text/html"}})});
`),
		"assistants/support/agent/agent.ts": []byte(`import {defineAgent} from "eve";
import {mockModel} from "eve/evals";
export default defineAgent({model:mockModel({modelId:"cost-workload",provider:"scenery-fixture",respond:()=>"cost-workload-ready"}),modelContextWindowTokens:4096});
`),
	}
	for _, name := range []string{"package.json", "package-lock.json", "index.ts", "agent/instructions.md"} {
		data, err := os.ReadFile(filepath.Join(p.repo, "testdata/assistant/assistants/support", name))
		if err != nil {
			return err
		}
		files["assistants/support/"+name] = data
	}
	for name, data := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
	}
	appendSource := func(name, addition string) error {
		file, err := os.OpenFile(filepath.Join(root, name), os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		_, writeErr := io.WriteString(file, addition)
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		return closeErr
	}
	if err := appendSource("library/package.scn", `
binding "list_mcp" {
  operation = operation.list
  execution = execution.list
  protocol = "mcp"
  delivery = "call"
  exposure = "application"
  authentication = std.authentication.inherit
  authorization = std.authorization.public
  pipeline = std.pipeline.empty
  mcp {
    name = "list"
    title = "List books"
    description = "List resource workload books."
    read_only = true
    destructive = false
    idempotent = true
    open_world = false
  }
}
export "list_mcp" { value = binding.list_mcp }
`); err != nil {
		return err
	}
	if err := appendSource("app.scn", `
mcp_server "cost" {
  capability "list" {
    binding = module.library.list_mcp
    name = "library__list"
    approval = "never"
  }
  max_input_bytes = 262144
  max_result_bytes = 1048576
}
assistant "support" {
  mcp_server = mcp_server.cost
  implementation {
    adapter = "eve"
    source = "./assistants/support"
    package = "./assistants/support/package.json"
    package_lock = "./assistants/support/package-lock.json"
  }
  surface {
    gateway = http_gateway.public_api
    path = "/assistants/support"
    authentication = std.authentication.none
    authorization = std.authorization.public
    pipeline = std.pipeline.empty
    session_access = "initiator"
    client = typescript_client.public_api
  }
}
`); err != nil {
		return err
	}
	if _, err := p.run(root, p.binary, "generate", "--target", "typescript_client.public_api", "--app-root", root, "-o", "json"); err != nil {
		return err
	}
	for _, args := range [][]string{{"add", "."}, {"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "-c", "user.name=Scenery release probe", "-c", "user.email=release-probe@example.invalid", "commit", "--quiet", "-m", "Frontend and deterministic Eve resource workload"}} {
		if _, err := p.run(root, "git", args...); err != nil {
			return err
		}
	}
	return nil
}

// Measure all recorded runtime roles, including service, frontend and assistant
// processes. Each resource sample verifies the captured owner fingerprints.
func (p *worktreeRuntimeProbe) captureCostOwners(cohort *worktreeCostCohort) error {
	var last error
	for started := time.Now(); time.Since(started) < 10*time.Second; {
		last = p.captureCostOwnersOnce(cohort)
		if last == nil {
			return nil
		}
		// A normal response can precede publication of the session's updated
		// service owners and retirement of its preceding generation. Wait for
		// a complete verifiable snapshot rather than dropping stale entries.
		if err := harnessWaitContext(p.ctx, 50*time.Millisecond); err != nil {
			return err
		}
	}
	return fmt.Errorf("resource owners did not settle: %w", last)
}

func (p *worktreeRuntimeProbe) captureCostOwnersOnce(cohort *worktreeCostCohort) error {
	cohort.pids, cohort.owners = nil, map[int]localagent.Owner{}
	cohort.roles = map[string]int{}
	add := func(role string, owner localagent.Owner) error {
		if _, exists := cohort.owners[owner.PID]; exists {
			return nil
		}
		if err := localagent.VerifyOwner(owner); err != nil {
			return fmt.Errorf("resource role %s PID %d: %w", role, owner.PID, err)
		}
		cohort.owners[owner.PID] = owner
		cohort.pids = append(cohort.pids, owner.PID)
		cohort.roles[role]++
		return nil
	}
	for _, root := range cohort.roots {
		session, err := p.liveSession(root)
		if err != nil {
			return err
		}
		if err := add("supervisor", session.Owner); err != nil {
			return err
		}
		frontend, assistant := false, false
		for name, process := range session.Processes {
			role := "service"
			switch {
			case name == "api":
				role = "api"
			case strings.HasPrefix(name, "frontend-"):
				role, frontend = "frontend", true
			case strings.HasPrefix(name, "assistant-"):
				role, assistant = "assistant", true
			}
			if err := add(role, process.Owner); err != nil {
				return err
			}
		}
		if !frontend || !assistant {
			return fmt.Errorf("resource workload is missing a managed frontend or assistant")
		}
		paths, err := localagent.PathsForWorktree(p.home, root)
		if err != nil {
			return err
		}
		client := localagent.NewClient(paths.Socket)
		stack, stackErr := p.waitVictoriaReady(client, 0)
		health, healthErr := client.Health(p.ctx)
		client.CloseIdleConnections()
		if stackErr != nil || healthErr != nil {
			return fmt.Errorf("resource control/observability identity: %v, %v", stackErr, healthErr)
		}
		if err := localagent.ValidateWorktreeHealth(health, paths); err != nil {
			return err
		}
		if err := add("control-agent", localagent.CaptureOwner(health.PID, "worktree cost control agent")); err != nil {
			return err
		}
		for name, owner := range stack.Owners {
			if err := add("victoria-"+name, owner); err != nil {
				return err
			}
		}
		// Include launcher descendants that are not separate session roles.
		usage, err := captureHarnessDevProcessTree(p.ctx, session.OwnerPID)
		if err != nil {
			return err
		}
		for _, pid := range usage.PIDs {
			if err := add("runtime-descendant", localagent.CaptureOwner(pid, "worktree cost runtime descendant")); err != nil {
				return err
			}
		}
	}
	sort.Ints(cohort.pids)
	return nil
}

func (p *worktreeRuntimeProbe) verifyCostAssistant(runtime detachedDevResult) (map[string]any, error) {
	started := time.Now()
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	httpClient := &http.Client{Jar: jar, Timeout: 30 * time.Second}
	defer httpClient.CloseIdleConnections()
	client := &harnessAssistantClient{ctx: p.ctx, api: worktreeProbeAPI(runtime), client: httpClient}
	var conversation, run string
	attempts := 0
	for begin := time.Now(); time.Since(begin) < 3*time.Minute; {
		attempts++
		conversation, run, err = client.create("cost readiness")
		if err == nil {
			break
		}
		if err := harnessWaitContext(p.ctx, 250*time.Millisecond); err != nil {
			return nil, err
		}
	}
	if err != nil {
		return nil, fmt.Errorf("assistant optional readiness: %w", err)
	}
	events, err := client.waitEvents(conversation, "deterministic assistant completion", func(events []harnessAssistantEvent) bool {
		return harnessAssistantHas(events, "assistant.run.completed", run)
	})
	return map[string]any{"conversation_id": conversation, "run_id": run, "terminal_events": len(events), "readiness_attempts": attempts, "duration_ms": time.Since(started).Milliseconds(), "boundary": "required API readiness through accepting and completing a deterministic assistant run; readiness polls are separate from test retry outcomes"}, err
}

// Unique handler responses establish real churn rather than touching source.
// Afterwards refresh the measured service owners and sample idle settling.
func (p *worktreeRuntimeProbe) churnCostCohort(cohort *worktreeCostCohort) (map[string]any, error) {
	result := map[string]any{"unique_edits_per_worktree": 20, "completed_edits_per_worktree": 0, "boundary": "unique normal-endpoint handler responses followed by three idle resource intervals; no performance gate"}
	before, err := p.costSample(*cohort)
	if err != nil {
		return result, err
	}
	result["before"] = before
	originals := make([][]byte, len(cohort.roots))
	for i, root := range cohort.roots {
		originals[i], err = os.ReadFile(filepath.Join(root, "library/service.go"))
		if err != nil {
			return result, err
		}
	}
	var observations []map[string]any
	client := &http.Client{Timeout: 2 * time.Second}
	defer client.CloseIdleConnections()
	for ordinal := 1; ordinal <= 20; ordinal++ {
		marker := fmt.Sprintf("cost-generation-%02d", ordinal)
		started := time.Now()
		observation := map[string]any{"generation": marker, "complete": false}
		observations = append(observations, observation)
		result["generations"] = observations
		for i, root := range cohort.roots {
			anchor := []byte("return contract.ListListed{Value: contract.BookList{Books: books}}, nil")
			changed := bytes.Replace(originals[i], anchor, []byte("books = append(books, contract.Book{BookId: \"cost-generation\", Title: \""+marker+"\"})\n\t"+string(anchor)), 1)
			if bytes.Equal(changed, originals[i]) {
				return result, fmt.Errorf("cost churn source anchor is missing")
			}
			if err := os.WriteFile(filepath.Join(root, "library/service.go"), changed, 0o644); err != nil {
				return result, err
			}
		}
		var served []map[string]string
		for _, runtime := range cohort.runtimes {
			ready := false
			var identity map[string]string
			for time.Since(started) < time.Minute {
				request, _ := http.NewRequestWithContext(p.ctx, http.MethodGet, worktreeProbeAPI(runtime)+"/books", nil)
				response, requestErr := client.Do(request)
				if requestErr == nil {
					body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
					_ = response.Body.Close()
					identity = map[string]string{}
					ready = response.StatusCode == 200 && bytes.Contains(body, []byte(marker))
					for _, name := range []string{"X-Scenery-Process-ID", "X-Scenery-Service-Process-ID", "X-Scenery-Service-Implementation-Revision", "X-Scenery-Implementation-Revision", "X-Scenery-Build-Input-Digest", "X-Scenery-Contract-Revision"} {
						identity[name] = response.Header.Get(name)
						ready = ready && identity[name] != ""
					}
					ready = ready && identity["X-Scenery-Process-ID"] == runtime.Session.AppPID
				}
				if ready {
					break
				}
				if err := harnessWaitContext(p.ctx, 50*time.Millisecond); err != nil {
					return result, err
				}
			}
			if !ready {
				return result, fmt.Errorf("cost churn never served %s", marker)
			}
			identity["app_root"] = runtime.Session.AppRoot
			served = append(served, identity)
			observation["served_identities"] = served
		}
		observation["all_worktrees_response_wall_ms"], observation["complete"] = time.Since(started).Milliseconds(), true
		result["completed_edits_per_worktree"] = ordinal
	}
	if err := p.captureCostOwners(cohort); err != nil {
		return result, err
	}
	after, err := p.measureCostPhase(*cohort, false)
	result["post_churn_idle"] = after
	return result, err
}
