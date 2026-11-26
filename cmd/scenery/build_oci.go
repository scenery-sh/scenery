package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"scenery.sh/internal/app"
	cp "scenery.sh/internal/clientprojection"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/pythonbuild"
	"scenery.sh/internal/rustbuild"
)

// ociPlatform is a Linux platform an application image can target, with the
// Go and Bun targets its workers are compiled for.
type ociPlatform struct {
	Name string
	Go   compiler.GoPlatform
	Bun  string
}

// ociPlatforms lists the supported image platforms. The amd64 Bun target is
// the baseline build, which runs on every x86-64 server CPU.
var ociPlatforms = map[string]ociPlatform{
	"linux/amd64": {Name: "linux/amd64", Go: compiler.GoPlatform{GOOS: "linux", GOARCH: "amd64"}, Bun: "bun-linux-x64-baseline"},
	"linux/arm64": {Name: "linux/arm64", Go: compiler.GoPlatform{GOOS: "linux", GOARCH: "arm64"}, Bun: "bun-linux-arm64"},
}

const (
	// ociRuntimeImage provides glibc, libgcc, CA certificates and the
	// unprivileged user 65532, and nothing else: the Host and its workers
	// are compiled against the same Debian 12 glibc.
	ociRuntimeImage = "gcr.io/distroless/cc-debian12:nonroot"
	// ociUVImage supplies the uv binary in the builder stage when an
	// application includes Python workers.
	ociUVImage    = "ghcr.io/astral-sh/uv:0.11.8"
	ociListenPort = 4000
)

// ociImagePattern is the Docker image reference grammar without a digest:
// an optional registry host and port, lowercase path components and an
// optional tag.
var ociImagePattern = regexp.MustCompile(`^(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?)*(?::[0-9]+)?/)?` +
	`[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*` +
	`(?::[A-Za-z0-9_][A-Za-z0-9_.-]{0,127})?$`)

// ociShellWord matches the names a generated Dockerfile places in a shell
// command without quoting.
var ociShellWord = regexp.MustCompile(`^[A-Za-z0-9._/+-]+$`)

// ociRequest is one validated `scenery build --oci` invocation.
type ociRequest struct {
	Image    string
	Platform ociPlatform
	// Context names the directory that receives the build context. Empty
	// builds the image with Docker from a temporary context instead.
	Context string
}

// parseOCIRequest validates the image reference and platform. An absent
// platform selects the host's architecture on Linux and amd64 elsewhere.
func parseOCIRequest(image, platform, contextDir string) (ociRequest, error) {
	if len(image) > 255 || !ociImagePattern.MatchString(image) {
		return ociRequest{}, fmt.Errorf("--oci %q is not an image reference such as registry.example.com/team/app:1.0", image)
	}
	if platform == "" {
		platform = defaultOCIPlatform(runtime.GOOS, runtime.GOARCH)
	}
	selected, ok := ociPlatforms[platform]
	if !ok {
		return ociRequest{}, fmt.Errorf("--platform %q is unsupported; use linux/amd64 or linux/arm64", platform)
	}
	request := ociRequest{Image: image, Platform: selected}
	if contextDir != "" {
		absolute, err := filepath.Abs(contextDir)
		if err != nil {
			return ociRequest{}, err
		}
		request.Context = absolute
	}
	return request, nil
}

func defaultOCIPlatform(goos, goarch string) string {
	if goos == "linux" && (goarch == "amd64" || goarch == "arm64") {
		return "linux/" + goarch
	}
	return "linux/amd64"
}

// ociRustWorker is one Rust worker the image's builder stage compiles.
// Placeholder stands for its executable digest in the bundle manifest until
// that stage computes it.
type ociRustWorker struct {
	Workspace   string
	Crate       string
	Binary      string
	Process     string
	Toolchain   string
	Placeholder string
}

// ociPythonWorker is one Python worker the image's builder stage packages
// against a standalone Linux CPython runtime installed by uv.
type ociPythonWorker struct {
	Workspace   string
	Directory   string
	Process     string
	Version     string
	HasProject  bool
	HasLock     bool
	EnvSlug     string
	Placeholder string
}

func ociPythonEnvSlug(workspace, version string) string {
	var out strings.Builder
	if workspace == "." || workspace == "" {
		out.WriteString("root")
	} else {
		for _, r := range workspace {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' {
				out.WriteRune(r)
			} else {
				out.WriteByte('_')
			}
		}
	}
	out.WriteByte('-')
	out.WriteString(version)
	return out.String()
}

// buildOCICommand builds the application's workers for a Linux platform and
// writes a self-contained Docker build context, then builds the image
// unless the context is the requested output.
func buildOCICommand(ctx context.Context, out io.Writer, compiled *compiler.Result, appRoot string, cfg app.Config, targetName string, request ociRequest, jsonOutput bool) error {
	bundle, rustWorkers, rustServices, pyWorkers, pyServices, err := buildOCIWorkerBundle(ctx, compiled, targetName, request.Platform)
	if err != nil {
		return goTargetSelectionCLIError(err)
	}
	frontend, err := selectWorkerFrontend(appRoot, cfg, compiled)
	if err != nil {
		return err
	}
	if frontend != nil {
		if bundle.Frontend, err = buildWorkerFrontend(ctx, compiled, *frontend); err != nil {
			return err
		}
	}
	destination := request.Context
	if destination == "" {
		temporary, err := os.MkdirTemp("", "app-image-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(temporary) }()
		destination = filepath.Join(temporary, "context")
	}
	exportRust := func(directory string, exclude ...string) error {
		return rustbuild.ExportWorkerSources(ctx, compiled.Root, rustServices, directory, exclude...)
	}
	exportPython := func(directory string, exclude ...string) error {
		return pythonbuild.ExportWorkerSources(compiled.Root, pyServices, directory, exclude...)
	}
	if err := writeOCIContext(bundle, rustWorkers, pyWorkers, exportRust, exportPython, destination, request.Platform); err != nil {
		return err
	}
	image := map[string]any{"image": request.Image, "platform": request.Platform.Name, "built": false}
	payload := map[string]any{"oci": image}
	if request.Context != "" {
		payload["output_path"] = destination
	} else {
		id, err := dockerBuildOCI(ctx, destination, request)
		if err != nil {
			return err
		}
		image["built"], image["image_id"] = true, id
	}
	if jsonOutput {
		return writeCLIJSON(out, withCLIPayloadIdentity("scenery.build.result", payload))
	}
	if request.Context != "" {
		_, err = fmt.Fprintln(out, destination)
		return err
	}
	_, err = fmt.Fprintf(out, "%s %s\n", request.Image, image["image_id"])
	return err
}

// buildOCIWorkerBundle builds the artifact workers of the application:
// Go services cross-compiled with cgo disabled, JavaScript and TypeScript
// services compiled by Bun for the platform, and Rust and Python services
// left to the image's builder stage.
func buildOCIWorkerBundle(ctx context.Context, result *compiler.Result, goTarget string, platform ociPlatform) (workerBundle, []ociRustWorker, []rustbuild.Service, []ociPythonWorker, []pythonbuild.Service, error) {
	bundle, selection, services, err := prepareWorkerBundle(ctx, result, "artifact")
	if err != nil {
		return workerBundle{}, nil, nil, nil, nil, err
	}
	builds, err := projectJSBuilds(result, bundle, "artifact")
	if err != nil {
		return workerBundle{}, nil, nil, nil, nil, err
	}
	for _, build := range builds {
		build.service.Platform = platform.Bun
		program, _, err := build.run(ctx, result.Root, false)
		if err != nil {
			return workerBundle{}, nil, nil, nil, nil, err
		}
		bundle.Programs = append(bundle.Programs, program)
	}
	pyBuilds, err := projectPythonBuilds(result, bundle, "artifact")
	if err != nil {
		return workerBundle{}, nil, nil, nil, nil, err
	}
	pyWorkers, pyServices, err := bundle.addOCIPythonWorkers(result, pyBuilds)
	if err != nil {
		return workerBundle{}, nil, nil, nil, nil, err
	}
	workers, err := bundle.addOCIRustWorkers(result, selection)
	if err != nil {
		return workerBundle{}, nil, nil, nil, nil, err
	}
	bundle.goPlatform = &platform.Go
	if _, err := bundle.addGoWorkers(ctx, result, "artifact", goTarget, nil, nil); err != nil {
		return workerBundle{}, nil, nil, nil, nil, err
	}
	if err := bundle.projectConfig(result); err != nil {
		return workerBundle{}, nil, nil, nil, nil, err
	}
	return bundle, workers, services, pyWorkers, pyServices, nil
}

// addOCIPythonWorkers declares each Python service at its place in the image.
// The builder stage installs the pinned Linux CPython interpreter via uv, syncs
// any pyproject.toml dependencies, packages the zipapp, and seals its digest.
func (bundle *workerBundle) addOCIPythonWorkers(result *compiler.Result, builds []workerPythonBuild) ([]ociPythonWorker, []pythonbuild.Service, error) {
	resources := cp.ResourcesByAddress(result.Manifest)
	var workers []ociPythonWorker
	var services []pythonbuild.Service
	for _, build := range builds {
		workspace := filepath.ToSlash(build.service.Workspace)
		directory := filepath.ToSlash(build.service.Directory)
		workspaceDir := filepath.Join(result.Root, build.service.Workspace)
		hasProject := isRegularFile(filepath.Join(workspaceDir, "pyproject.toml"))
		hasLock := isRegularFile(filepath.Join(workspaceDir, "uv.lock"))
		worker := ociPythonWorker{
			Workspace:   workspace,
			Directory:   directory,
			Process:     build.program.Process,
			Version:     build.service.Version,
			HasProject:  hasProject,
			HasLock:     hasLock,
			EnvSlug:     ociPythonEnvSlug(workspace, build.service.Version),
			Placeholder: fmt.Sprintf("@python-worker-%d@", len(workers)),
		}
		for _, word := range []string{worker.Workspace, worker.Directory, worker.Process, worker.Version, worker.EnvSlug} {
			if !ociShellWord.MatchString(word) || strings.Contains(word, "..") {
				return nil, nil, fmt.Errorf("python service %s has a name an image build cannot use: %q", build.program.Service, word)
			}
		}
		workers = append(workers, worker)
		services = append(services, build.service)
		bundle.Programs = append(bundle.Programs, workerProgram{
			Service: build.program.Service, Process: build.program.Process, Binary: "services/" + build.program.Process,
			Digest: worker.Placeholder, Runtime: "python", Databases: build.program.Databases,
			Identity: workerProcessIdentity{
				ContractRevision:       compiler.ServiceContractRevision(result.Manifest, resources[build.program.Service], nil),
				ImplementationRevision: worker.Placeholder, BuildInputDigest: worker.Placeholder, Target: "python",
			},
		})
	}
	return workers, services, nil
}

func isRegularFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

// addOCIRustWorkers declares each Rust service at its place in the image.
// Its digest, which is also its implementation identity, is a placeholder
// the builder stage replaces once it has compiled the executable.
func (bundle *workerBundle) addOCIRustWorkers(result *compiler.Result, selection map[string]rustbuild.Service) ([]ociRustWorker, error) {
	resources := cp.ResourcesByAddress(result.Manifest)
	var workers []ociRustWorker
	for _, service := range bundle.Host.Workers {
		selected, exists := selection[service.Address]
		if !exists {
			continue
		}
		worker := ociRustWorker{
			Workspace: filepath.ToSlash(selected.Workspace), Crate: selected.Crate, Binary: rustbuild.BinaryName(selected),
			Process: service.Process, Toolchain: selected.Toolchain, Placeholder: fmt.Sprintf("@rust-worker-%d@", len(workers)),
		}
		for _, word := range []string{worker.Workspace, worker.Crate, worker.Binary, worker.Process, worker.Toolchain} {
			if !ociShellWord.MatchString(word) || strings.Contains(word, "..") {
				return nil, fmt.Errorf("rust service %s has a name an image build cannot use: %q", service.Address, word)
			}
		}
		workers = append(workers, worker)
		bundle.Programs = append(bundle.Programs, workerProgram{
			Service: service.Address, Process: service.Process, Binary: "services/" + service.Process,
			Digest: worker.Placeholder, Runtime: "rust",
			Identity: workerProcessIdentity{
				ContractRevision:       compiler.ServiceContractRevision(result.Manifest, resources[service.Address], nil),
				ImplementationRevision: worker.Placeholder, BuildInputDigest: worker.Placeholder, Target: "rust",
			},
		})
	}
	return workers, nil
}

// writeOCIContext writes the build context: `bundle/` with the manifest,
// prebuilt worker executables and static frontend, `host/` with the Host's
// Cargo sources, `rust/` with the Rust workers' sources, `python/` with the
// Python workers' sources, and the Dockerfile. Modes and modification times
// are normalized, so the same inputs produce the same bytes. A previous
// context at destination is replaced; any other existing entry is refused.
func writeOCIContext(bundle workerBundle, rustWorkers []ociRustWorker, pyWorkers []ociPythonWorker, exportRust, exportPython func(string, ...string) error, destination string, platform ociPlatform) error {
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	if _, err := ociContextExists(destination); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, ".oci-context-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	root := filepath.Join(staging, "bundle")
	if err := os.MkdirAll(filepath.Join(root, "services"), 0o755); err != nil {
		return err
	}
	if bundle.Frontend != nil {
		frontend := *bundle.Frontend
		relative := filepath.Join("frontends", frontend.Name)
		if err := os.CopyFS(filepath.Join(root, relative), os.DirFS(frontend.Root)); err != nil {
			return err
		}
		frontend.Root = filepath.ToSlash(relative)
		bundle.Frontend = &frontend
	}
	bundle.Programs = slices.Clone(bundle.Programs)
	for i, program := range bundle.Programs {
		if program.Runtime == "rust" || program.Runtime == "python" {
			continue
		}
		relative := filepath.ToSlash(filepath.Join("services", program.Process))
		if err := copyWorkerExecutable(program.Binary, filepath.Join(root, filepath.FromSlash(relative))); err != nil {
			return err
		}
		bundle.Programs[i].Binary = relative
	}
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), append(data, '\n'), 0o644); err != nil {
		return err
	}
	if err := rustbuild.ExportHostSources(filepath.Join(staging, "host")); err != nil {
		return err
	}
	if len(rustWorkers) > 0 {
		if err := exportRust(filepath.Join(staging, "rust"), staging, destination); err != nil {
			return err
		}
	}
	if len(pyWorkers) > 0 {
		if err := exportPython(filepath.Join(staging, "python"), staging, destination); err != nil {
			return err
		}
	}
	dockerfile := renderOCIDockerfile(platform, rustbuild.HostToolchain, rustWorkers, pyWorkers)
	if err := os.WriteFile(filepath.Join(staging, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		return err
	}
	if err := normalizeOCIContext(staging); err != nil {
		return err
	}
	return publishOCIContext(staging, destination)
}

// ociContextEpoch is the modification time of every context entry.
var ociContextEpoch = time.Unix(0, 0)

func normalizeOCIContext(root string) error {
	var directories []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			directories = append(directories, path)
			return os.Chmod(path, 0o755)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("build context entry %s is not a regular file", path)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if info.Mode()&0o111 != 0 {
			mode = 0o755
		}
		if err := os.Chmod(path, mode); err != nil {
			return err
		}
		return os.Chtimes(path, ociContextEpoch, ociContextEpoch)
	})
	if err != nil {
		return err
	}
	// Children first, so setting a directory's time is not undone.
	for i := len(directories) - 1; i >= 0; i-- {
		if err := os.Chtimes(directories[i], ociContextEpoch, ociContextEpoch); err != nil {
			return err
		}
	}
	return nil
}

func publishOCIContext(staging, destination string) error {
	exists, err := ociContextExists(destination)
	if err != nil {
		return err
	}
	if !exists {
		return os.Rename(staging, destination)
	}
	retired := staging + ".retired"
	if err := os.Rename(destination, retired); err != nil {
		return err
	}
	if err := os.Rename(staging, destination); err != nil {
		return errors.Join(err, os.Rename(retired, destination))
	}
	return os.RemoveAll(retired)
}

const ociGeneratedDockerfileHeader = "# syntax=docker/dockerfile:1\n# Generated by `scenery build --oci`."

// ociContextExists reports whether destination holds a previous generated
// build context and refuses any other existing entry.
func ociContextExists(destination string) (bool, error) {
	info, err := os.Lstat(destination)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, name := range []string{"Dockerfile", filepath.Join("bundle", "manifest.json")} {
		if entry, err := os.Lstat(filepath.Join(destination, name)); !info.IsDir() || err != nil || !entry.Mode().IsRegular() {
			return false, preconditionErrorf("build context destination %s exists and is not an image build context", destination)
		}
	}
	dockerfile, err := os.ReadFile(filepath.Join(destination, "Dockerfile"))
	if err != nil || !strings.HasPrefix(string(dockerfile), ociGeneratedDockerfileHeader) {
		return false, preconditionErrorf("build context destination %s exists and is not a generated image build context", destination)
	}
	return true, nil
}

// renderOCIDockerfile writes a two-stage Dockerfile. The builder stage
// compiles the Host, every Rust worker, and every Python worker for the
// target platform and seals their digests into the manifest; the runtime
// stage holds the bundle and runs the Host as an unprivileged user on port 4000.
func renderOCIDockerfile(platform ociPlatform, hostToolchain string, rustWorkers []ociRustWorker, pyWorkers []ociPythonWorker) string {
	var file strings.Builder
	write := func(format string, args ...any) { fmt.Fprintf(&file, format, args...) }
	write("# syntax=docker/dockerfile:1\n")
	write("# Generated by `scenery build --oci`. Build it with:\n")
	write("#   docker buildx build --platform %s --tag <image> --load .\n\n", platform.Name)
	write("FROM rust:%s-bookworm AS build\n", hostToolchain)
	write("WORKDIR /build\n")
	write("COPY host/ host/\n")
	write("RUN cargo +%s build --locked --release \\\n", hostToolchain)
	write("      --manifest-path host/scenery-host/Cargo.toml --target-dir /target/host \\\n")
	write(" && install -D -m 0755 /target/host/release/scenery-host /out/scenery-host\n")
	copiedManifest := false
	if len(rustWorkers) > 0 {
		rustWorkers = slices.Clone(rustWorkers)
		slices.SortFunc(rustWorkers, func(a, b ociRustWorker) int { return strings.Compare(a.Process, b.Process) })
		var toolchains []string
		for _, worker := range rustWorkers {
			if worker.Toolchain != hostToolchain && !slices.Contains(toolchains, worker.Toolchain) {
				toolchains = append(toolchains, worker.Toolchain)
			}
		}
		slices.Sort(toolchains)
		for _, toolchain := range toolchains {
			write("RUN rustup toolchain install %s --profile minimal\n", toolchain)
		}
		write("COPY bundle/manifest.json /out/manifest.json\n")
		copiedManifest = true
		write("COPY rust/ rust/\n")
		for _, worker := range rustWorkers {
			executable := "/out/services/" + worker.Process
			write("RUN cd /build/rust/%s \\\n", worker.Workspace)
			write(" && cargo +%s build --locked --release --package %s --bin %s --target-dir /target/workers \\\n", worker.Toolchain, worker.Crate, worker.Binary)
			write(" && install -D -m 0755 /target/workers/release/%s %s \\\n", worker.Binary, executable)
			write(" && sed -i \"s|%s|sha256:$(sha256sum %s | cut -d ' ' -f 1)|g\" /out/manifest.json\n", worker.Placeholder, executable)
		}
	}
	hasPythonEnvs := false
	if len(pyWorkers) > 0 {
		pyWorkers = slices.Clone(pyWorkers)
		slices.SortFunc(pyWorkers, func(a, b ociPythonWorker) int { return strings.Compare(a.Process, b.Process) })
		write("COPY --from=%s /uv /usr/local/bin/uv\n", ociUVImage)
		var versions []string
		for _, worker := range pyWorkers {
			if !slices.Contains(versions, worker.Version) {
				versions = append(versions, worker.Version)
			}
		}
		slices.Sort(versions)
		for _, version := range versions {
			write("RUN UV_PYTHON_INSTALL_DIR=/opt/scenery-python UV_PYTHON_PREFERENCE=only-managed uv python install --no-bin %s \\\n", version)
			write(" && chmod -R a+rX /opt/scenery-python\n")
		}
		if !copiedManifest {
			write("COPY bundle/manifest.json /out/manifest.json\n")
		}
		write("COPY python/ python/\n")
		syncedEnvs := map[string]bool{}
		for _, worker := range pyWorkers {
			if !worker.HasProject || syncedEnvs[worker.EnvSlug] {
				continue
			}
			syncedEnvs[worker.EnvSlug] = true
			hasPythonEnvs = true
			envPath := "/opt/scenery-python-envs/" + worker.EnvSlug
			frozen := ""
			if worker.HasLock {
				frozen = " --frozen"
			}
			write("RUN cd /build/python/%s \\\n", worker.Workspace)
			write(" && UV_PYTHON_INSTALL_DIR=/opt/scenery-python UV_PYTHON_PREFERENCE=only-managed UV_PROJECT_ENVIRONMENT=%s UV_LINK_MODE=copy uv sync --python %s --no-install-project --no-dev%s \\\n", envPath, worker.Version, frozen)
			write(" && chmod -R a+rX %s\n", envPath)
		}
		for _, worker := range pyWorkers {
			executable := "/out/services/" + worker.Process
			builder := "/build/python/" + worker.Directory + "/scenerycontract/_build.py"
			if worker.HasProject {
				interpreter := "/opt/scenery-python-envs/" + worker.EnvSlug + "/bin/python"
				write("RUN %s -B %s --output %s --interpreter %s \\\n", interpreter, builder, executable, interpreter)
			} else {
				write("RUN INTERP=\"$(UV_PYTHON_INSTALL_DIR=/opt/scenery-python UV_PYTHON_PREFERENCE=only-managed uv python find %s)\" \\\n", worker.Version)
				write(" && \"$INTERP\" -B %s --output %s --interpreter \"$INTERP\" \\\n", builder, executable)
			}
			write(" && sed -i \"s|%s|sha256:$(sha256sum %s | cut -d ' ' -f 1)|g\" /out/manifest.json\n", worker.Placeholder, executable)
		}
	}
	write("\nFROM %s\n", ociRuntimeImage)
	write("WORKDIR /app\n")
	write("COPY bundle/ /app/\n")
	if len(pyWorkers) > 0 {
		write("COPY --from=build /opt/scenery-python /opt/scenery-python\n")
		if hasPythonEnvs {
			write("COPY --from=build /opt/scenery-python-envs /opt/scenery-python-envs\n")
		}
	}
	write("COPY --from=build /out/ /app/\n")
	write("USER 65532:65532\n")
	write("EXPOSE %d\n", ociListenPort)
	write("ENTRYPOINT [\"/app/scenery-host\", \"--listen\", \"0.0.0.0:%d\"]\n", ociListenPort)
	return file.String()
}

// dockerBuildOCI builds and loads the image with Docker Buildx and returns
// its image ID.
func dockerBuildOCI(ctx context.Context, contextDir string, request ociRequest) (string, error) {
	docker, err := execLookPath("docker")
	if err != nil {
		return "", preconditionErrorf("building an image requires Docker with Buildx; use --oci-context <dir> to write the build context instead")
	}
	idFile := filepath.Join(filepath.Dir(contextDir), "image-id")
	command := exec.CommandContext(ctx, docker, "buildx", "build", "--platform", request.Platform.Name,
		"--tag", request.Image, "--iidfile", idFile, "--load", contextDir)
	command.Stdout, command.Stderr = os.Stderr, os.Stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("docker buildx build %s: %w", request.Image, err)
	}
	id, err := os.ReadFile(idFile)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(id)), nil
}
