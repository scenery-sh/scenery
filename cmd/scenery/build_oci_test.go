package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/pythonbuild"
	"scenery.sh/internal/rustbuild"
	"scenery.sh/internal/workerplan"
)

func TestOCIImageReferencesAndPlatforms(t *testing.T) {
	t.Parallel()

	for _, image := range []string{"app", "app:1.0", "ghcr.io/acme/app:v1", "localhost:5000/team/app:latest", "registry.example.com:443/a/b_c/d-e:Tag_1.2-x", "a__b/c"} {
		if _, err := parseOCIRequest(image, "linux/arm64", ""); err != nil {
			t.Errorf("rejected %q: %v", image, err)
		}
	}
	for _, image := range []string{"", "App:1", "app:", "app:-x", "app@sha256:" + strings.Repeat("a", 64), "/app", "app//x", "a b", "app:" + strings.Repeat("t", 129), "-app"} {
		if _, err := parseOCIRequest(image, "", ""); err == nil {
			t.Errorf("accepted %q", image)
		}
	}
	for _, test := range []struct{ goos, goarch, want string }{
		{"linux", "arm64", "linux/arm64"}, {"linux", "amd64", "linux/amd64"},
		{"darwin", "arm64", "linux/amd64"}, {"linux", "riscv64", "linux/amd64"},
	} {
		if got := defaultOCIPlatform(test.goos, test.goarch); got != test.want {
			t.Errorf("defaultOCIPlatform(%s, %s) = %s, want %s", test.goos, test.goarch, got, test.want)
		}
	}
	request, err := parseOCIRequest("app:1", "linux/amd64", "image")
	if err != nil || !filepath.IsAbs(request.Context) || request.Platform.Go.GOARCH != "amd64" || request.Platform.Bun != "bun-linux-x64-baseline" {
		t.Fatalf("request = %#v, %v", request, err)
	}
	if arm := ociPlatforms["linux/arm64"]; arm.Go.GOOS != "linux" || arm.Bun != "bun-linux-arm64" {
		t.Fatalf("arm64 platform = %#v", arm)
	}
}

func TestOCIDockerfileForGoOnlyApplication(t *testing.T) {
	t.Parallel()

	dockerfile := renderOCIDockerfile(ociPlatforms["linux/amd64"], "1.99.1", nil, nil)
	if dockerfile != renderOCIDockerfile(ociPlatforms["linux/amd64"], "1.99.1", nil, nil) {
		t.Fatal("rendering is not deterministic")
	}
	for _, want := range []string{
		"# syntax=docker/dockerfile:1\n",
		"docker buildx build --platform linux/amd64 --tag <image> --load .",
		"FROM rust:1.99.1-bookworm AS build\n",
		"RUN cargo +1.99.1 build --locked --release \\\n      --manifest-path host/scenery-host/Cargo.toml --target-dir /target/host \\\n",
		" && install -D -m 0755 /target/host/release/scenery-host /out/scenery-host\n",
		"FROM gcr.io/distroless/cc-debian12:nonroot\n",
		"COPY bundle/ /app/\nCOPY --from=build /out/ /app/\nUSER 65532:65532\nEXPOSE 4000\n",
		`ENTRYPOINT ["/app/scenery-host", "--listen", "0.0.0.0:4000"]` + "\n",
	} {
		if !strings.Contains(dockerfile, want) {
			t.Errorf("Dockerfile lacks %q:\n%s", want, dockerfile)
		}
	}
	for _, absent := range []string{"COPY rust/", "rustup", "sed -i"} {
		if strings.Contains(dockerfile, absent) {
			t.Errorf("Go-only Dockerfile contains %q", absent)
		}
	}
}

func TestOCIDockerfileCompilesAndSealsRustWorkers(t *testing.T) {
	t.Parallel()

	workers := []ociRustWorker{
		{Workspace: "native", Crate: "pricing", Binary: "scenery-worker-quote", Process: "quote", Toolchain: "1.95.0", Placeholder: "@rust-worker-1@"},
		{Workspace: ".", Crate: "ledger", Binary: "scenery-worker-ledger", Process: "ledger", Toolchain: "1.99.1", Placeholder: "@rust-worker-0@"},
	}
	dockerfile := renderOCIDockerfile(ociPlatforms["linux/arm64"], "1.99.1", workers, nil)
	reversed := renderOCIDockerfile(ociPlatforms["linux/arm64"], "1.99.1", []ociRustWorker{workers[1], workers[0]}, nil)
	if dockerfile != reversed {
		t.Fatal("worker order changes the Dockerfile")
	}
	for _, want := range []string{
		"--platform linux/arm64",
		"RUN rustup toolchain install 1.95.0 --profile minimal\n",
		"COPY bundle/manifest.json /out/manifest.json\nCOPY rust/ rust/\n",
		"RUN cd /build/rust/. \\\n && cargo +1.99.1 build --locked --release --package ledger --bin scenery-worker-ledger --target-dir /target/workers \\\n" +
			" && install -D -m 0755 /target/workers/release/scenery-worker-ledger /out/services/ledger \\\n" +
			` && sed -i "s|@rust-worker-0@|sha256:$(sha256sum /out/services/ledger | cut -d ' ' -f 1)|g" /out/manifest.json` + "\n",
		"RUN cd /build/rust/native \\\n && cargo +1.95.0 build --locked --release --package pricing --bin scenery-worker-quote",
	} {
		if !strings.Contains(dockerfile, want) {
			t.Errorf("Dockerfile lacks %q:\n%s", want, dockerfile)
		}
	}
	if strings.Contains(dockerfile, "rustup toolchain install 1.99.1") {
		t.Error("the host toolchain is installed again")
	}
	if strings.Index(dockerfile, "--package ledger") > strings.Index(dockerfile, "--package pricing") {
		t.Error("workers are not ordered by process")
	}
}

// A polyglot bundle: a cross-compiled Go and Bun executable, a Rust worker
// the builder stage compiles, and a static frontend.
func ociFixture(t *testing.T) (workerBundle, []ociRustWorker, func(string, ...string) error) {
	t.Helper()
	source := t.TempDir()
	write := func(relative, contents string, mode os.FileMode) string {
		path := filepath.Join(source, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), mode); err != nil {
			t.Fatal(err)
		}
		return path
	}
	goWorker := write("bin/go-worker", "go executable", 0o700)
	bunWorker := write("bin/bun-worker", "bun executable", 0o700)
	write("web/dist/index.html", "<!doctype html>", 0o600)
	write("app/native/Cargo.toml", "[package]\nname = \"pricing\"\n", 0o600)
	write("app/native/scenerycontract/quote_worker.rs", "fn main() {}\n", 0o600)
	write("app/Cargo.toml", "[workspace]\nmembers = [\"native\"]\n", 0o600)
	write("app/Cargo.lock", "version = 4\n", 0o600)
	write("app/.scenery/gen/rust-sdk/Cargo.toml", "[package]\nname = \"scenery-sdk\"\n", 0o600)
	services := []rustbuild.Service{{ModuleDirectory: "native", Workspace: ".", Crate: "pricing", ServiceName: "quote", Toolchain: "1.99.1"}}
	bundle := workerBundle{
		Version: 1, ContractRevision: "sha256:contract", Reports: workerReportIdentity{SchemaRevision: "sha256:report", ProducerVersion: "test"},
		Host: workerHostPlan{Name: "shop", Workers: []workerplan.WorkerServicePlan{}},
		Programs: []workerProgram{
			{Service: "shop/service/api", Process: "api", Binary: goWorker, Digest: "sha256:go", Runtime: "go"},
			{Service: "shop/service/web", Process: "web", Binary: bunWorker, Digest: "sha256:bun", Runtime: "ts"},
			{Service: "shop/service/quote", Process: "quote", Binary: "services/quote", Digest: "@rust-worker-0@", Runtime: "rust",
				Identity: workerProcessIdentity{ImplementationRevision: "@rust-worker-0@", BuildInputDigest: "@rust-worker-0@", Target: "rust"}},
		},
		Frontend: &workerFrontend{Name: "web", Root: filepath.Join(source, "web", "dist"), Reserved: []string{"/api"}},
	}
	workers := []ociRustWorker{{Workspace: ".", Crate: "pricing", Binary: rustbuild.BinaryName(services[0]), Process: "quote", Toolchain: "1.99.1", Placeholder: "@rust-worker-0@"}}
	export := func(directory string, exclude ...string) error {
		return rustbuild.ExportWorkerSources(t.Context(), filepath.Join(source, "app"), services, directory, exclude...)
	}
	return bundle, workers, export
}

// treeDigest hashes every path, mode, modification time and content.
func treeDigest(t *testing.T, root string) string {
	t.Helper()
	hash := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(root, path)
		_, _ = fmt.Fprintf(hash, "%s %s %d\n", filepath.ToSlash(relative), info.Mode(), info.ModTime().Unix())
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			_, _ = hash.Write(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func TestOCIContextIsSelfContainedAndReproducible(t *testing.T) {
	t.Parallel()

	bundle, workers, export := ociFixture(t)
	output := t.TempDir()
	first, second := filepath.Join(output, "first"), filepath.Join(output, "second")
	platform := ociPlatforms["linux/amd64"]
	for _, destination := range []string{first, second} {
		if err := writeOCIContext(bundle, workers, nil, export, nil, destination, platform); err != nil {
			t.Fatal(err)
		}
	}
	if treeDigest(t, first) != treeDigest(t, second) {
		t.Fatal("the same inputs produced different build contexts")
	}
	if bundle.Programs[0].Binary == "services/api" || bundle.Frontend.Root == "frontends/web" {
		t.Fatal("writing the context changed the caller's bundle")
	}
	read := func(relative string) string {
		data, err := os.ReadFile(filepath.Join(first, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	var manifest struct {
		Programs []struct{ Binary, Digest, Runtime string }
		Frontend struct{ Root string }
	}
	if err := json.Unmarshal([]byte(read("bundle/manifest.json")), &manifest); err != nil {
		t.Fatal(err)
	}
	binaries := map[string]string{}
	for _, program := range manifest.Programs {
		binaries[program.Runtime] = program.Binary + " " + program.Digest
	}
	if binaries["go"] != "services/api sha256:go" || binaries["ts"] != "services/web sha256:bun" || binaries["rust"] != "services/quote @rust-worker-0@" || manifest.Frontend.Root != "frontends/web" {
		t.Fatalf("manifest = %#v", manifest)
	}
	if read("bundle/services/api") != "go executable" || read("bundle/frontends/web/index.html") != "<!doctype html>" {
		t.Fatal("prebuilt artifacts were not copied")
	}
	if !strings.Contains(read("rust/native/Cargo.toml"), `name = "scenery-worker-quote"`) || read("rust/.scenery/gen/rust-sdk/Cargo.toml") == "" {
		t.Fatal("the Rust worker sources lack their bin declaration or SDK")
	}
	if !strings.Contains(read("host/scenery-host/Cargo.toml"), `name = "scenery-host"`) || read("host/scenery-host/Cargo.lock") == "" || read("host/scenery-sdk/scenery-wire/Cargo.toml") == "" {
		t.Fatal("the Host sources are incomplete")
	}
	if read("Dockerfile") != renderOCIDockerfile(platform, rustbuild.HostToolchain, workers, nil) {
		t.Fatal("the context Dockerfile differs from its rendering")
	}
	for relative, mode := range map[string]os.FileMode{"bundle/services/api": 0o755, "bundle/manifest.json": 0o644, "host/scenery-host/src/main.rs": 0o644, "bundle": 0o755 | fs.ModeDir} {
		info, err := os.Stat(filepath.Join(first, relative))
		if err != nil || info.Mode() != mode || info.ModTime().Unix() != 0 {
			t.Errorf("%s: mode %v time %v (%v)", relative, info.Mode(), info.ModTime(), err)
		}
	}

}

func TestOCIContextReplacesOnlyAPreviousContext(t *testing.T) {
	t.Parallel()

	bundle, workers, export := ociFixture(t)
	platform := ociPlatforms["linux/arm64"]
	output := t.TempDir()
	context := filepath.Join(output, "image")
	for range 2 {
		if err := writeOCIContext(bundle, workers, nil, export, nil, context, platform); err != nil {
			t.Fatal(err)
		}
	}
	occupied := filepath.Join(output, "occupied")
	if err := os.MkdirAll(occupied, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeOCIContext(bundle, workers, nil, export, nil, occupied, platform); err == nil || !strings.Contains(err.Error(), "not an image build context") {
		t.Fatalf("occupied destination: %v", err)
	}
	// A hand-authored directory that happens to contain a Dockerfile and
	// bundle/manifest.json must never be overwritten.
	handAuthored := filepath.Join(output, "hand-authored")
	if err := os.MkdirAll(filepath.Join(handAuthored, "bundle"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(handAuthored, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(handAuthored, "bundle", "manifest.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeOCIContext(bundle, workers, nil, export, nil, handAuthored, platform); err == nil || !strings.Contains(err.Error(), "not a generated image build context") {
		t.Fatalf("hand-authored destination: %v", err)
	}
	entries, err := os.ReadDir(output)
	if err != nil || len(entries) != 3 {
		t.Fatalf("staging was left behind: %v %v", entries, err)
	}
}

func TestOCIContextOfAGoOnlyApplication(t *testing.T) {
	t.Parallel()

	bundle, _, export := ociFixture(t)
	bundle.Programs = bundle.Programs[:1]
	context := filepath.Join(t.TempDir(), "image")
	if err := writeOCIContext(bundle, nil, nil, export, nil, context, ociPlatforms["linux/amd64"]); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(context, "rust")); !os.IsNotExist(err) {
		t.Fatalf("Go-only context has Rust sources: %v", err)
	}
	dockerfile, err := os.ReadFile(filepath.Join(context, "Dockerfile"))
	if err != nil || strings.Contains(string(dockerfile), "COPY rust/") || strings.Contains(string(dockerfile), "COPY python/") {
		t.Fatalf("Go-only Dockerfile: %v\n%s", err, dockerfile)
	}
}

func TestOCIDockerfileAndContextForPythonWorkers(t *testing.T) {
	t.Parallel()

	pyWorkers := []ociPythonWorker{
		{Workspace: "ml", Directory: "ml", Process: "rank", Version: "3.12.9", HasProject: true, HasLock: true, EnvSlug: "ml-3.12.9", Placeholder: "@python-worker-1@"},
		{Workspace: ".", Directory: "echo", Process: "echo", Version: "3.14.6", HasProject: false, EnvSlug: "root-3.14.6", Placeholder: "@python-worker-0@"},
	}
	dockerfile := renderOCIDockerfile(ociPlatforms["linux/amd64"], "1.99.1", nil, pyWorkers)
	reversed := renderOCIDockerfile(ociPlatforms["linux/amd64"], "1.99.1", nil, []ociPythonWorker{pyWorkers[1], pyWorkers[0]})
	if dockerfile != reversed {
		t.Fatal("Python worker order changes the Dockerfile")
	}
	for _, want := range []string{
		"COPY --from=" + ociUVImage + " /uv /usr/local/bin/uv\n",
		"uv python install --no-bin 3.12.9",
		"uv python install --no-bin 3.14.6",
		"COPY bundle/manifest.json /out/manifest.json\nCOPY python/ python/\n",
		"UV_PROJECT_ENVIRONMENT=/opt/scenery-python-envs/ml-3.12.9 UV_LINK_MODE=copy uv sync --python 3.12.9 --no-install-project --no-dev --frozen",
		"/opt/scenery-python-envs/ml-3.12.9/bin/python -B /build/python/ml/scenerycontract/_build.py --output /out/services/rank",
		`sed -i "s|@python-worker-0@|sha256:$(sha256sum /out/services/echo | cut -d ' ' -f 1)|g" /out/manifest.json`,
		"COPY --from=build /opt/scenery-python /opt/scenery-python\n",
		"COPY --from=build /opt/scenery-python-envs /opt/scenery-python-envs\n",
	} {
		if !strings.Contains(dockerfile, want) {
			t.Errorf("Python Dockerfile lacks %q:\n%s", want, dockerfile)
		}
	}

	appRoot := t.TempDir()
	write := func(rel, content string) {
		path := filepath.Join(appRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("echo/service.py", "def new_service(input):\n    return object()\n")
	write("echo/scenerycontract/_build.py", "# builder\n")
	write("echo/test_service.py", "# test\n")
	write("ml/pyproject.toml", "[project]\nname = \"ml\"\n")
	write("ml/uv.lock", "version = 1\n")
	write("ml/service.py", "def new_service(input):\n    return object()\n")
	write("ml/scenerycontract/_build.py", "# builder\n")
	services := []pythonbuild.Service{
		{PythonWorkerTarget: compiler.PythonWorkerTarget{Workspace: ".", Directory: "echo", Entry: "service.py", Version: "3.14.6"}, Name: "echo/service/echo"},
		{PythonWorkerTarget: compiler.PythonWorkerTarget{Workspace: "ml", Directory: "ml", Entry: "service.py", Version: "3.12.9"}, Name: "ml/service/rank"},
	}
	bundle := workerBundle{
		Version: 1, ContractRevision: "sha256:contract", Reports: workerReportIdentity{SchemaRevision: "sha256:report", ProducerVersion: "test"},
		Host: workerHostPlan{Name: "pyapp", Workers: []workerplan.WorkerServicePlan{}},
		Programs: []workerProgram{
			{Service: "echo/service/echo", Process: "echo", Binary: "services/echo", Digest: "@python-worker-0@", Runtime: "python"},
			{Service: "ml/service/rank", Process: "rank", Binary: "services/rank", Digest: "@python-worker-1@", Runtime: "python"},
		},
	}
	exportPy := func(directory string, exclude ...string) error {
		return pythonbuild.ExportWorkerSources(appRoot, services, directory, exclude...)
	}
	contextDir := filepath.Join(appRoot, "oci-out")
	if err := writeOCIContext(bundle, nil, pyWorkers, nil, exportPy, contextDir, ociPlatforms["linux/amd64"]); err != nil {
		t.Fatal(err)
	}
	for _, present := range []string{"python/echo/service.py", "python/echo/scenerycontract/_build.py", "python/ml/pyproject.toml", "python/ml/uv.lock"} {
		if _, err := os.Stat(filepath.Join(contextDir, filepath.FromSlash(present))); err != nil {
			t.Errorf("missing %s in Python OCI context: %v", present, err)
		}
	}
	for _, absent := range []string{"python/echo/test_service.py", "python/oci-out", "bundle/services/echo"} {
		if _, err := os.Stat(filepath.Join(contextDir, filepath.FromSlash(absent))); !os.IsNotExist(err) {
			t.Errorf("unexpected %s in Python OCI context: %v", absent, err)
		}
	}
}

func TestOCIContextInsideAppRootExcludesSelfAndRootSecrets(t *testing.T) {
	t.Parallel()

	bundle, workers, _ := ociFixture(t)
	appRoot := t.TempDir()
	write := func(relative, contents string) {
		path := filepath.Join(appRoot, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("Cargo.toml", "[workspace]\nmembers = [\"native\"]\n")
	write("Cargo.lock", "version = 4\n")
	write("native/Cargo.toml", "[package]\nname = \"pricing\"\n")
	write("native/scenerycontract/quote_worker.rs", "fn main() {}\n")
	write(".scenery/gen/rust-sdk/Cargo.toml", "[package]\nname = \"scenery-sdk\"\n")
	write("prod-config.json", `{"secrets":{"token":"super-secret"}}`)
	services := []rustbuild.Service{{ModuleDirectory: "native", Workspace: ".", Crate: "pricing", ServiceName: "quote", Toolchain: "1.99.1"}}
	export := func(directory string, exclude ...string) error {
		return rustbuild.ExportWorkerSources(t.Context(), appRoot, services, directory, exclude...)
	}
	contextDir := filepath.Join(appRoot, "oci-out")
	if err := writeOCIContext(bundle, workers, nil, export, nil, contextDir, ociPlatforms["linux/amd64"]); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(contextDir, "rust", "prod-config.json")); !os.IsNotExist(err) {
		t.Fatalf("root secrets JSON leaked into Rust OCI context: %v", err)
	}
	if _, err := os.Stat(filepath.Join(contextDir, "rust", "oci-out")); !os.IsNotExist(err) {
		t.Fatalf("OCI context directory recursed into rust/: %v", err)
	}
}

func TestOCIBuildResultsMatchTheCheckedSchema(t *testing.T) {
	t.Parallel()

	schema := filepath.Join("..", "..", "docs", "schemas", "scenery.build.result.schema.json")
	image := map[string]any{"image": "registry.example.com/shop:1.0", "platform": "linux/arm64", "built": false}
	for name, payload := range map[string]map[string]any{
		"bundle":  {"output_path": "/srv/app/dist/bundle"},
		"context": {"output_path": "/srv/app/dist/image", "oci": image},
		"image":   {"oci": map[string]any{"image": "shop:1", "platform": "linux/amd64", "built": true, "image_id": "sha256:" + strings.Repeat("ab", 32)}},
	} {
		if problems := validateHarnessJSONSchemaFile(schema, roundTrip(t, withCLIPayloadIdentity("scenery.build.result", payload))); len(problems) > 0 {
			t.Errorf("%s: %v", name, problems)
		}
	}
	for name, payload := range map[string]map[string]any{
		"empty":    {},
		"platform": {"oci": map[string]any{"image": "shop:1", "platform": "linux/s390x", "built": false}},
		"id":       {"oci": map[string]any{"image": "shop:1", "platform": "linux/amd64", "built": true, "image_id": "latest"}},
	} {
		if problems := validateHarnessJSONSchemaFile(schema, roundTrip(t, withCLIPayloadIdentity("scenery.build.result", payload))); len(problems) == 0 {
			t.Errorf("%s: invalid payload passed the schema", name)
		}
	}
}

func roundTrip(t *testing.T, value any) any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}
