package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"scenery.sh/internal/devtelemetry"
)

func runHarnessWorkerIncrementalStep(ctx context.Context, repo string) harnessStep {
	started := time.Now()
	summary, err := runHarnessWorkerIncremental(ctx, repo)
	step := harnessStep{Name: "worker incremental replacement probe", Summary: summary, DurationMS: time.Since(started).Milliseconds(), OK: err == nil}
	if err != nil {
		step.Error = err.Error()
	}
	return step
}

func prepareHarnessWorkerIncremental(repo, app string) error {
	if err := copyHarnessMultiserviceFixture(repo, app); err != nil {
		return err
	}
	declarations := filepath.Join(app, "app.scn")
	if err := harnessReplaceInFile(declarations, `"echo/scenerycontract",`,
		`"echo/scenerycontract", "idle/scenerycontract", "pricing/scenerycontract", "backup/scenerycontract", "echo_ts/scenerycontract",`); err != nil {
		return err
	}
	write := func(name string, data []byte) error {
		path := filepath.Join(app, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, data, 0o600)
	}
	read := func(fixture, name string) ([]byte, error) {
		return os.ReadFile(filepath.Join(repo, "testdata/apps", fixture, name))
	}
	for _, file := range []string{"package.scn", "api.go"} {
		data, err := read("multiservice", "echo/"+file)
		if err != nil {
			return err
		}
		source := strings.NewReplacer("echo", "idle", "Echo", "Idle").Replace(string(data))
		source = strings.ReplaceAll(source, "\n\t\"example.com/multiservice/internal/text\"", "")
		source = strings.ReplaceAll(source, `text.Label("idle", input.Message)`, `"idle:" + input.Message`)
		if err := write("idle/"+file, []byte(source)); err != nil {
			return err
		}
	}
	for _, file := range []string{"package.scn", "Cargo.toml", "src/lib.rs"} {
		data, err := read("rust-only", "pricing/"+file)
		if err != nil {
			return err
		}
		if err := write("pricing/"+file, data); err != nil {
			return err
		}
		backup := strings.NewReplacer("pricing", "backup", "Pricing", "Backup", `"/quote"`, `"/backup"`).Replace(string(data))
		if err := write("backup/"+file, []byte(backup)); err != nil {
			return err
		}
	}
	for _, file := range []string{"package.scn", "service.ts"} {
		data, err := read("ts-only", "echo/"+file)
		if err != nil {
			return err
		}
		source := strings.ReplaceAll(string(data), `"/echo"`, `"/echo-ts"`)
		if err := write("echo_ts/"+file, []byte(source)); err != nil {
			return err
		}
	}
	if err := write("Cargo.toml", []byte("[workspace]\nresolver = \"3\"\nmembers = [\"pricing\", \"backup\"]\n")); err != nil {
		return err
	}
	lock, err := read("rust-only", "pricing/Cargo.lock")
	if err != nil {
		return err
	}
	_, packages, ok := bytes.Cut(lock, []byte("[[package]]\nname = \"pricing\""))
	if !ok {
		return fmt.Errorf("fixture Cargo.lock is missing its local packages")
	}
	packages, _, ok = bytes.Cut(packages, []byte("[[package]]\nname = \"proc-macro2\""))
	if !ok {
		return fmt.Errorf("fixture Cargo.lock layout changed")
	}
	backup := "[[package]]\nname = \"backup\"" + strings.ReplaceAll(string(packages), "pricing", "backup")
	if err := write("Cargo.lock", append(lock, backup...)); err != nil {
		return err
	}
	appSCN, err := os.ReadFile(declarations)
	if err != nil {
		return err
	}
	return write("app.scn", append(appSCN, `
rust_workspace "application" { root = "." }
rust_toolchain "application" { version = "1.99.1" }
rust_target "development" {
  role = "development"
  workspace = rust_workspace.application
  toolchain = rust_toolchain.application
}
module "idle" {
  source = "./idle"
  inputs = { gateway = http_gateway.public_api }
}
module "pricing" {
  source = "./pricing"
  inputs = { gateway = http_gateway.public_api }
}
module "backup" {
  source = "./backup"
  inputs = { gateway = http_gateway.public_api }
}
js_workspace "application" { root = "echo_ts" }
js_toolchain "application" { version = "1.3.14" }
js_target "development" {
  role = "development"
  workspace = js_workspace.application
  toolchain = js_toolchain.application
}
module "echo_ts" {
  source = "./echo_ts"
  inputs = { gateway = http_gateway.public_api }
}
`...))
}

type harnessWorkerReply struct {
	Body                      map[string]string
	PID, Host, Binary, Digest string
	Generation                string
}

type harnessWorkerRebuild struct {
	Declarations bool
	Go           bool
	GoLinks      []string `json:"go_links"`
	Cargo        []string
	JS           []string
	Rebuilt      []string
	Skipped      int
}

func runHarnessWorkerIncremental(parent context.Context, repo string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(parent, 4*time.Minute)
	defer cancel()
	root, err := os.MkdirTemp("", "scenery-worker-incremental-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(root) }()
	app := filepath.Join(root, "app")
	if err := prepareHarnessWorkerIncremental(repo, app); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	address := listener.Addr().String()
	_ = listener.Close()
	output, err := os.Create(filepath.Join(root, "up.log"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = output.Close() }()
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	command := commandTreeContext(ctx, harnessLocalSceneryBinaryPath(repo), "up", "--app-root", app, "--port", port)
	command.Dir, command.Env = app, harnessAppEnv(filepath.Join(root, "agent"))
	command.Stdout, command.Stderr = output, output
	if err := command.Start(); err != nil {
		return nil, err
	}
	defer func() {
		_ = command.Process.Signal(os.Interrupt)
		_ = command.Wait()
	}()
	httpClient := &http.Client{Timeout: time.Second}
	defer httpClient.CloseIdleConnections()
	call := func(path, body string) (harnessWorkerReply, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+address+path, strings.NewReader(body))
		if err != nil {
			return harnessWorkerReply{}, err
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := httpClient.Do(req)
		if err != nil {
			return harnessWorkerReply{}, err
		}
		defer func() { _ = res.Body.Close() }()
		reply := harnessWorkerReply{PID: res.Header.Get("X-App-Worker-Pid"), Host: res.Header.Get("X-App-Host-Pid"), Generation: res.Header.Get("X-App-Generation")}
		if res.StatusCode != http.StatusOK || reply.PID == "" || reply.Host == "" {
			return reply, fmt.Errorf("%s returned HTTP %d without worker identity", path, res.StatusCode)
		}
		return reply, json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&reply.Body)
	}
	routes := map[string]string{"/echo": `{"message":"probe"}`, "/greet": `{"name":"probe"}`, "/idle": `{"message":"probe"}`, "/quote": `{"amount":"7"}`, "/backup": `{"amount":"7"}`, "/echo-ts": `{"amount":"7"}`}
	readWorkers := func() (map[string]harnessWorkerReply, error) {
		workers := make(map[string]harnessWorkerReply)
		for route, body := range routes {
			reply, err := call(route, body)
			if err != nil {
				return nil, err
			}
			process, err := exec.CommandContext(ctx, "ps", "-p", reply.PID, "-o", "args=").Output()
			if err != nil {
				return nil, err
			}
			// argv is the absolute executable followed by the private launch
			// config. Split at the latter so executable paths may have spaces.
			binary, _, ok := strings.Cut(strings.TrimSpace(string(process)), " /tmp/app-workers-")
			if !ok {
				return nil, fmt.Errorf("worker %s has no executable in ps: %s", route, process)
			}
			data, err := os.ReadFile(binary)
			if err != nil {
				return nil, err
			}
			reply.Binary, reply.Digest = binary, fmt.Sprintf("%x", sha256.Sum256(data))
			workers[route] = reply
		}
		return workers, nil
	}
	wait := func(timeout time.Duration, ready func() bool) error {
		ticker := time.NewTicker(15 * time.Millisecond)
		defer ticker.Stop()
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		for {
			if ready() {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				log, _ := os.ReadFile(output.Name())
				var lifecycle []string
				for line := range strings.SplitSeq(string(log), "\n") {
					if !strings.Contains(line, " TRC ") && !strings.Contains(line, "code=unavailable") {
						lifecycle = append(lifecycle, line)
					}
				}
				return fmt.Errorf("worker proof timed out: %s", tailString(strings.Join(lifecycle, "\n"), 5000))
			case <-ticker.C:
			}
		}
	}
	if err := wait(160*time.Second, func() bool {
		_, err := call("/backup", routes["/backup"])
		return err == nil
	}); err != nil {
		return nil, err
	}
	before, err := readWorkers()
	if err != nil {
		return nil, err
	}
	wire := exec.CommandContext(ctx, "bun", filepath.Join(repo, "sdk/js/scenery-sdk/tests/worker-wire.mjs"),
		filepath.Join(app, "echo_ts/scenerycontract/codec.ts"))
	if output, err := wire.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("JavaScript wire conformance: %w: %s", err, output)
	}
	summary := map[string]any{"js_wire_cases": 23}
	for _, trial := range []struct {
		name, file, old, new, route, key, want string
		replaced, links, cargo, js             []string
		declarations                           bool
	}{
		{"Go", "echo/api.go", `text.Label("echo",`, `text.Label("next",`, "/echo", "message", "next:probe", []string{"/echo"}, []string{"echo/service/echo"}, nil, nil, false},
		{"Rust", "pricing/src/lib.rs", "amount: input.amount,", "amount: sdk::Int64(input.amount.0 + 1),", "/quote", "amount", "8", []string{"/quote"}, nil, []string{"pricing"}, nil, false},
		{"Go shared", "internal/text/text.go", `service + ":" + message`, `service + "::" + message`, "/echo", "message", "next::probe",
			[]string{"/echo", "/greet"}, []string{"echo/service/echo", "greeter/service/greeter"}, nil, nil, false},
		{"TypeScript", "echo_ts/service.ts", "value: input", "value: { ...input, amount: input.amount + 1n }", "/echo-ts", "amount", "8",
			[]string{"/echo-ts"}, nil, nil, []string{"echo_ts/service/echo"}, false},
		{"declaration", "app.scn", "application \"multiservice\" {", "# declaration refresh\napplication \"multiservice\" {", "", "", "", nil, nil, nil, nil, true},
	} {
		stat, err := output.Stat()
		if err != nil {
			return nil, err
		}
		source := filepath.Join(app, trial.file)
		metadata, err := os.Stat(source)
		if err != nil {
			return nil, err
		}
		if err := harnessReplaceInFile(source, trial.old, trial.new); err != nil {
			return nil, err
		}
		// Equal-length replacement with restored mtime still changes bytes.
		if trial.name == "Go" {
			if err := os.Chtimes(source, metadata.ModTime(), metadata.ModTime()); err != nil {
				return nil, err
			}
		}
		var timing harnessWorkerRebuild
		if err := wait(20*time.Second, func() bool {
			if trial.route != "" {
				reply, err := call(trial.route, routes[trial.route])
				if err != nil || reply.Body[trial.key] != trial.want {
					return false
				}
			}
			log, err := os.ReadFile(output.Name())
			if err != nil || int64(len(log)) < stat.Size() {
				return false
			}
			for line := range strings.SplitSeq(string(log[stat.Size():]), "\n") {
				if record, ok := strings.CutPrefix(line, "worker rebuild "); ok && json.Unmarshal([]byte(record), &timing) == nil {
					return true
				}
			}
			return false
		}); err != nil {
			return nil, fmt.Errorf("%s: %w", trial.name, err)
		}
		slices.Sort(timing.GoLinks)
		if timing.Declarations != trial.declarations || timing.Go != (len(trial.links) != 0 || trial.declarations) ||
			!slices.Equal(timing.GoLinks, trial.links) || !slices.Equal(timing.Cargo, trial.cargo) || !slices.Equal(timing.JS, trial.js) ||
			timing.Skipped != len(routes)-len(trial.replaced) || len(timing.Rebuilt) != len(trial.replaced) {
			return nil, fmt.Errorf("%s selected unexpected builds: %+v", trial.name, timing)
		}
		after, err := readWorkers()
		if err != nil {
			return nil, err
		}
		for route, worker := range before {
			current := after[route]
			changed := slices.Contains(trial.replaced, route)
			if worker.Host != current.Host || (worker.PID != current.PID) != changed ||
				(worker.Binary != current.Binary) != changed || (worker.Digest != current.Digest) != changed {
				return nil, fmt.Errorf("%s unexpectedly changed worker %s: %+v -> %+v", trial.name, route, worker, current)
			}
		}
		summary[trial.name] = map[string]any{"builds": timing, "before": before, "after": after}
		if trial.name == "TypeScript" {
			event, err := verifyWorkerRebuildTelemetry(ctx, repo, app, filepath.Join(root, "agent"), after[trial.route].Generation)
			if err != nil {
				return nil, err
			}
			summary["rebuild_telemetry"] = event
		}
		before = after
	}
	if err := harnessReplaceInFile(filepath.Join(app, "echo", "package.scn"), `path          = "/echo"`, `path          = "/renamed"`); err != nil {
		return nil, err
	}
	if err := wait(10*time.Second, func() bool {
		log, err := os.ReadFile(output.Name())
		return err == nil && bytes.Contains(log, []byte("application contract changed; restart the application"))
	}); err != nil {
		return nil, err
	}
	after, err := readWorkers()
	if err != nil {
		return nil, err
	}
	for route, worker := range before {
		if current := after[route]; worker.PID != current.PID || worker.Generation != current.Generation || worker.Digest != current.Digest {
			return nil, fmt.Errorf("contract change altered live worker %s", route)
		}
	}
	summary["contract_change_requires_restart"] = true
	return summary, nil
}

func verifyWorkerRebuildTelemetry(ctx context.Context, repo, app, home, generation string) (devtelemetry.Event, error) {
	command := commandTreeContext(ctx, harnessLocalSceneryBinaryPath(repo), "telemetry", "-o", "json")
	command.Dir, command.Env = app, harnessAppEnv(home)
	output, err := command.Output()
	if err != nil {
		return devtelemetry.Event{}, fmt.Errorf("read worker rebuild telemetry: %w", err)
	}
	var query struct {
		Events []devtelemetry.Event `json:"events"`
	}
	if err := decodeCLIJSON(output, &query); err != nil {
		return devtelemetry.Event{}, err
	}
	for _, event := range query.Events {
		if event.Rebuild == nil || fmt.Sprint(event.Generation) != generation {
			continue
		}
		build := event.Rebuild
		if build.OK && build.TriggerKind == "body_only" && slices.Equal(build.RebuiltModules, []string{"echo_ts"}) &&
			len(build.SkippedModules) == 5 && build.EditToReadyMS != nil && *build.EditToReadyMS >= build.ReadyMS {
			return event, nil
		}
	}
	return devtelemetry.Event{}, fmt.Errorf("missing a successful TypeScript-only rebuild event for generation %s", generation)
}
