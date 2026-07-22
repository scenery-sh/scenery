# Frozen Toolchain Manifest and Managed Tool Store
This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`, `Decision Log`, and `Outcomes & Retrospective` current as work proceeds. Maintain this file according to `PLANS.md`.
## Purpose / Big Picture
Scenery should have a root-level, checked-in toolchain manifest that freezes every Scenery-owned external executable, image, plugin, and source lock reference for the current Scenery version.
A contributor, release binary, or CI job should be able to answer:
1. Which external tools belong to this Scenery version?
2. Which exact binary versions should be used?
3. Which Docker images are allowed?
4. Which source dependency lock files define Go, UI, or generated-runtime dependencies?
5. Where are local controlled binaries installed?
6. Which command downloads or verifies the required local toolchain?
7. Whether the current machine is accidentally relying on a system `PATH` binary.
After this change, bumping a managed tool version in the manifest changes the artifact key. The next `scenery toolchain sync`, or the next command that needs that tool, downloads the matching binary or verifies/pulls the matching Docker image into a Scenery-owned location.
Scenery must not silently use binaries found in the system `PATH`. The allowed sources are:
- the Scenery-managed toolchain store;
- explicit per-tool env overrides only for documented operator-facing tools that are intentionally not hidden dev-service substrate;
- explicit external-service URLs where the command is configured to reuse an external service;
- manifest-pinned Docker images.
The user-visible surface is:
    scenery toolchain list --json
    scenery toolchain sync --json
    scenery toolchain verify --json
    scenery toolchain path --tool <name>
The root manifest is:
    scenery.toolchain.json
The default local store is:
    <app-root>/.scenery/toolchain/
The global override is:
    SCENERY_TOOLCHAIN_DIR=/some/controlled/path
Downloaded binaries, extracted tool homes, image presence metadata, and installation metadata are local state and must not be committed.
## Progress
- [x] (2026-06-01 02:05Z) Initial `toolchain`-named ExecPlan drafted.
- [x] (2026-06-01 09:02Z) Add root toolchain manifest and schema.
- [x] (2026-06-01 09:02Z) Add managed toolchain resolver and artifact store.
- [x] (2026-06-01 09:02Z) Add `scenery toolchain` CLI commands.
- [x] (2026-06-01 09:02Z) Migrate Grafana and Victoria to the managed resolver.
- [x] (2026-06-01 09:02Z) Audit legacy async runtime, Postgres, frontend worker runtimes, and other dev services for implicit `PATH` or unpinned Docker image resolution.
- [x] (2026-06-01 09:02Z) Add docs, schemas, environment contract updates, and knowledge index updates.
- [x] (2026-06-01 09:02Z) Add tests and self-harness coverage.
- [x] (2026-06-24) Promote ZeroFS into the frozen toolchain manifest and remove `SCENERY_DEV_ZEROFS_BIN`, `SCENERY_LEGACY_ASYNC_RUNTIME_BIN`, `SCENERY_DEV_POSTGRES_BIN`, and `SCENERY_DEV_POSTGRES_INITDB` as managed-substrate binary overrides. ZeroFS and legacy async runtime CLI now resolve through the managed toolchain store, and managed Postgres starts from the manifest-pinned Docker image unless an explicit admin URL or external database mode is configured.
- [x] (2026-06-24) Add `scenery upgrade` to fetch a verified release binary, replace the local executable, and run the upgraded binary's managed toolchain sync for already-installed tools by default or the whole manifest with `--toolchain all`.
- [x] (2026-07-22) Complete validation and update retrospective against the
  current singular toolchain surface; the worktree-local self-harness is the
  final repository gate.
## Surprises & Discoveries
- Observation: Scenery already has a partial internal pin file at `internal/devtools/versions.json`.
  Evidence: It currently pins Grafana, Grafana plugins, VictoriaMetrics, VictoriaLogs, and VictoriaTraces.
- Observation: The existing internal pin file is not enough for the desired contract.
  Evidence: It is not root-level, is named as internal devtool state, does not describe download URLs per platform, does not describe Docker images, does not expose source locks, and does not define the local artifact store contract.
- Observation: `internal/devtools/versions.go` embeds and parses `internal/devtools/versions.json`.
  Evidence: The parser validates the existing internal schema and exposes helper functions such as `PinnedVersions()` and `GrafanaPluginPreinstallSync()`.
- Observation: Victoria binary resolution currently allows implicit system `PATH` fallback.
  Evidence: `cmd/scenery/victoria.go` resolves explicit env override, then a local bin directory, then `exec.LookPath`, then download.
- Observation: Grafana binary resolution currently allows implicit system `PATH` fallback.
  Evidence: `cmd/scenery/grafana.go` resolves explicit env override and local paths, probes versions, and can fall back to `PATH`.
- Observation: `.scenery/` is already ignored.
  Evidence: `.gitignore` ignores `.scenery/`, so `.scenery/toolchain/` is appropriate for local downloaded state.
- Observation: legacy async runtime CLI release assets include platform tarballs and a checksum sidecar.
  Evidence: `https://github.com/legacy-async-runtimeio/cli/releases/download/v1.7.0/checksums.txt` lists `legacy-async-runtime_cli_1.7.0_linux_amd64.tar.gz` and `legacy-async-runtime_cli_1.7.0_darwin_arm64.tar.gz`.
- Observation: The old `internal/devtools/versions.json` pin file can be deleted instead of generated as compatibility output.
  Evidence: `internal/devtools.PinnedVersions()` now derives Grafana, plugin, and Victoria versions from the bundled `scenery.toolchain.json` manifest while preserving the old Go API.
- Observation: Docker image refs remain tag-only in this migration step.
  Evidence: `scenery toolchain verify --strict --images --json` exits non-zero and reports `status: "invalid"` for Victoria and Postgres image refs because they intentionally have no digest yet and are marked `stability: "unstable"`.
- Observation: Source package-manager lookups are distinct from managed toolchain artifacts.
  Evidence: remaining `bun`, `npm`, `node`, and `tsx` lookups are used for app-local scripts, frontends, UI builds, or generated TypeScript workers and are documented as source/package-manager tooling rather than hidden Scenery-managed downloads.
- Observation: The remaining validation failure is unrelated to the toolchain contract and comes from the existing self-harness Go timing gate.
  Evidence: after replacing the duplicate `TestRunHarnessParallelDevStep` real parallel-dev check with a fast wrapper test, `scenery harness self --json --write` still reports only `full Go suite took 8.911s, over 7.000s target`; the real parallel-dev validation still runs as its own self-harness step.
## Decision Log
- Decision: Use **toolchain** as the user-facing noun.
  Rationale: `deps` and `dependencies` are overloaded with Go modules, npm packages, and generated package locks. `toolchain` better describes Scenery-controlled executables, images, plugins, and local runtime tools.
  Date/Author: 2026-06-01 / Codex.
- Decision: Add the root manifest as `scenery.toolchain.json`.
  Rationale: The user wants root-level visibility and per-Scenery-version freezing. The file should be obvious to humans and agents inspecting the repository root.
  Date/Author: 2026-06-01 / Codex.
- Decision: Use schema version `scenery.toolchain.v1`.
  Rationale: Scenery already uses versioned machine-readable contracts. The toolchain manifest should be validated and evolvable.
  Date/Author: 2026-06-01 / Codex.
- Decision: Use `.scenery/toolchain/` as the default local store.
  Rationale: `.scenery/` is already ignored local state. A dedicated `toolchain` subtree is explicit and separates managed tools from generated app metadata, build artifacts, harness output, Victoria state, and other local runtime files.
  Date/Author: 2026-06-01 / Codex.
- Decision: Use `SCENERY_TOOLCHAIN_DIR` as the global store override.
  Rationale: Operators and agents need full control over where binaries live, including shared cache directories and hermetic CI workspaces.
  Date/Author: 2026-06-01 / Codex.
- Decision: Use `SCENERY_TOOLCHAIN_DOWNLOAD=0` as the global automatic-download disable switch.
  Rationale: Some environments must be offline or audit downloads explicitly. A global switch gives deterministic failure instead of surprise network access.
  Date/Author: 2026-06-01 / Codex.
- Decision: Remove implicit `PATH` fallback for managed toolchain artifacts.
  Rationale: The user explicitly wants Scenery to avoid system/path binaries even when available. Managed tools should resolve from explicit env override, Scenery store, or manifest-driven download.
  Date/Author: 2026-06-01 / Codex.
- Decision: Do not expose binary-path env overrides for hidden managed dev-service substrate.
  Rationale: ZeroFS, legacy async runtime CLI, and managed Postgres are Scenery-owned toolchain/runtime substrate. Agents and target apps should get the frozen version from `scenery.toolchain.json` through the managed store, not configure individual binary paths. Explicit external-service URLs remain valid when the user intentionally points Scenery at infrastructure they own.
  Date/Author: 2026-06-24 / Codex.
- Decision: Treat Go modules and UI package-manager files as `source_locks`, not managed toolchain downloads.
  Rationale: `go.mod`, `go.sum`, `package.json`, and package lock files already freeze source dependency graphs. The toolchain manifest should reference and report those lock surfaces without duplicating the entire dependency graph.
  Date/Author: 2026-06-01 / Codex.
- Decision: Make the release binary expose the bundled toolchain manifest SHA.
  Rationale: Each Scenery release should be auditable. `scenery version --json` should prove which toolchain manifest the binary was built with.
  Date/Author: 2026-06-01 / Codex.
- Decision: Make `scenery upgrade` run post-install toolchain sync from the upgraded binary.
  Rationale: The release binary is the source of truth for the frozen manifest. Syncing through the upgraded executable ensures changed ZeroFS, legacy async runtime, Postgres, and other Scenery-owned substrate versions come from the new release instead of the old binary or ambient system tools. The default limits work to tools already present locally; `--toolchain all` is the explicit full pull.
  Date/Author: 2026-06-24 / Codex.
- Decision: Leave tag-only image refs as explicit unstable migration metadata for this change.
  Rationale: Digest-pinning every image is desirable, but strict verification already fails on tag-only refs and the manifest exposes the instability instead of hiding it.
  Date/Author: 2026-06-01 / Codex.
## Outcomes & Retrospective
Completed 2026-07-22. The frozen manifest, managed artifact store, checksum and
archive verification, Docker runner injection, version metadata, CLI, upgrade
sync, and no-ambient-PATH contract are the current implementation. Later
plans removed Grafana, ZeroFS, and the legacy async runtime; those historical
entries do not remain as compatibility paths. Victoria runs the manifest-owned
managed binaries. Optional tag-only Victoria image references are deliberately
non-installable and rejected by strict image verification, so they do not
weaken the executable toolchain identity. Test-suite timing was resolved by
ExecPlan 0050 and no longer blocks this plan's harness acceptance.
## Context and Orientation
Scenery is a Go-native service runtime and local development platform. App roots are marked by `.scenery.json`. The CLI starts local development services, generated app processes, observability sidecars, managed Postgres services, legacy async runtime workers, frontends, and dashboards.
The relevant repository files and packages are:
- `go.mod`
  - Go module dependency manifest.
- `go.sum`
  - Go module dependency lock file.
- `ui/package.json`
  - Dashboard/UI package manifest, if present in the working tree.
- UI lock file, such as `ui/bun.lock`, `ui/bun.lockb`, or another current lock file
  - UI package dependency lock surface.
- `.goreleaser.yaml`
  - Scenery CLI release build configuration.
- `internal/devtools/versions.json`
  - Current internal pin file for Grafana, Grafana plugins, and Victoria components.
- `internal/devtools/versions.go`
  - Current parser and embedded accessor for the internal devtool version file.
- `cmd/scenery/victoria.go`
  - Starts and downloads VictoriaMetrics, VictoriaLogs, and VictoriaTraces for local observability.
- `cmd/scenery/grafana.go`
  - Starts and downloads Grafana for local observability.
- `cmd/scenery/legacy-async-runtime_dev.go`
  - Starts local legacy async runtime dev server when configured.
- Postgres managed-service code
  - Starts or reuses managed Postgres for dev services.
- `docs/local-contract.md`
  - CLI, JSON, artifact path, and stability contract.
- `docs/environment.md`
  - Scenery-owned environment variables.
- `docs/agent-guide.md`
  - Agent-facing Scenery workflow docs.
- `SKILL.md`
  - Installable Scenery skill for agents working inside Scenery apps.
- `docs/schemas/`
  - JSON schemas for machine-readable Scenery contracts.
- `docs/knowledge.json`
  - Indexed documentation metadata.
- `.gitignore`
  - Local-state policy. `.scenery/` is already ignored.
Current behavior already pins some devtool versions, but the pinning is implementation-private and incomplete. The new toolchain contract must be root-visible, release-frozen, schema-validated, and used by runtime startup paths.
The term **toolchain artifact** means a Scenery-managed external thing that Scenery may need to run local development or runtime support commands. Examples include Grafana, VictoriaMetrics, VictoriaLogs, VictoriaTraces, legacy async runtime CLI, Postgres images, and plugins.
The term **source lock** means an existing dependency lock surface such as `go.sum` or a UI lock file. Source locks are listed in `scenery.toolchain.json` for inventory and release audit purposes, but their dependency graph remains owned by the native package manager.
## Milestones
### Milestone 1: Add root toolchain manifest and schema
Add:
    scenery.toolchain.json
    docs/schemas/scenery.toolchain.v1.schema.json
The initial manifest should migrate the information from `internal/devtools/versions.json` and add enough metadata for deterministic local resolution.
The first manifest should cover:
- Grafana
- Grafana plugins
- VictoriaMetrics
- VictoriaLogs
- VictoriaTraces
- legacy async runtime CLI, if Scenery owns auto-starting it
- Postgres image or binary channel, if Scenery owns starting it
- any generated TypeScript worker runtime tooling that Scenery installs or invokes directly
- source locks:
  - `go.mod`
  - `go.sum`
  - `ui/package.json`
  - the current UI lock file, if present
Proposed manifest shape:
    {
      "schema_version": "scenery.toolchain.v1",
      "manifest_version": 1,
      "source_locks": [
        {
          "name": "go-modules",
          "kind": "go-modules",
          "manifest": "go.mod",
          "lock": "go.sum"
        },
        {
          "name": "dashboard-ui",
          "kind": "package-manager",
          "manager": "bun",
          "manifest": "ui/package.json",
          "lock": "ui/bun.lock"
        }
      ],
      "artifacts": [
        {
          "name": "victoria-metrics",
          "kind": "binary",
          "version": "v1.141.0",
          "license": "Apache-2.0",
          "default_binary": "victoria-metrics-prod",
          "binaries": ["victoria-metrics-prod", "victoria-metrics"],
          "platforms": {
            "linux/amd64": {
              "archive": "tar.gz",
              "url": "https://github.com/VictoriaMetrics/VictoriaMetrics/releases/download/v1.141.0/victoria-metrics-linux-amd64-v1.141.0.tar.gz",
              "sha256": "<fill-before-enforce>",
              "extract": "victoria-metrics-prod"
            },
            "darwin/arm64": {
              "archive": "tar.gz",
              "url": "https://github.com/VictoriaMetrics/VictoriaMetrics/releases/download/v1.141.0/victoria-metrics-darwin-arm64-v1.141.0.tar.gz",
              "sha256": "<fill-before-enforce>",
              "extract": "victoria-metrics-prod"
            }
          },
          "images": [
            {
              "ref": "victoriametrics/victoria-metrics:v1.141.0",
              "digest": "sha256:<fill-before-enforce>",
              "optional": true
            }
          ]
        }
      ]
    }
Schema rules:
- Reject unknown fields.
- Require non-empty `schema_version`.
- Require exact schema version `scenery.toolchain.v1`.
- Require non-empty artifact names.
- Require valid artifact kinds.
- Require non-empty versions for managed artifacts.
- Require valid platform keys in `goos/goarch` format.
- Require non-empty URLs for downloadable platform artifacts.
- Require SHA-256 checksums for enforced downloads.
- Require extraction targets for archive artifacts.
- Require image refs for image artifacts.
- Prefer image digests when Docker images are managed.
- Allow temporary `checksum_status: "pending"` only behind an explicit development-only test path, not in final accepted release state.
### Milestone 2: Add internal toolchain package
Create:
    internal/toolchain
This package owns:
- parsing `scenery.toolchain.json`;
- validating schema version;
- rejecting unknown fields;
- computing manifest SHA-256;
- resolving current platform from `runtime.GOOS` and `runtime.GOARCH`;
- selecting artifacts by name;
- selecting platform downloads;
- computing local install paths;
- downloading archives;
- verifying SHA-256 before extraction;
- extracting only expected files;
- rejecting archive path traversal;
- making extracted binaries executable;
- writing install metadata;
- verifying existing installs;
- reporting machine-readable status;
- optionally verifying or pulling Docker images through an injectable Docker runner.
The default store layout should be versioned and platform-specific:
    .scenery/toolchain/
      manifest/
        scenery.toolchain.sha256
      artifacts/
        victoria-metrics/
          v1.141.0/
            linux-amd64/
              bin/
                victoria-metrics-prod
              archive/
                victoria-metrics-linux-amd64-v1.141.0.tar.gz
              install.json
        grafana/
          13.0.1+security-01/
            darwin-arm64/
              home/
              bin/
                grafana
              install.json
      images/
        index.json
The package should expose APIs shaped like:
    type Manifest struct { ... }
    type Store struct { ... }
    type ArtifactStatus struct { ... }
    func ParseManifest(data []byte) (Manifest, error)
    func LoadBundledManifest() (Manifest, error)
    func BundledManifestBytes() []byte
    func BundledManifestSHA256() string
    func DefaultStoreDir(appRoot string) string
    func NewStore(dir string, manifest Manifest) (*Store, error)
    func (s *Store) List(ctx context.Context, opts ListOptions) (Status, error)
    func (s *Store) Sync(ctx context.Context, opts SyncOptions) (Status, error)
    func (s *Store) Verify(ctx context.Context, opts VerifyOptions) (Status, error)
    func (s *Store) Path(ctx context.Context, artifactName string, platform Platform) (PathStatus, error)
Installation must be atomic:
1. create temp directory under the target store;
2. download archive to temp file;
3. verify SHA-256;
4. extract into temp directory;
5. verify expected binary paths;
6. chmod executable files;
7. rename into final version/platform directory;
8. write `install.json`.
`install.json` should look like:
    {
      "schema_version": "scenery.toolchain.install.v1",
      "name": "victoria-metrics",
      "version": "v1.141.0",
      "platform": "linux/amd64",
      "manifest_sha256": "...",
      "source_url": "...",
      "source_sha256": "...",
      "installed_at": "..."
    }
Retries must be safe. Partial temp directories should be deleted or ignored on the next run.
### Milestone 3: Embed the release-frozen manifest
The Scenery binary must know which toolchain manifest it was built with.
Preferred implementation:
- Add `internal/toolchain/manifest_gen.go`.
- Generate it from root `scenery.toolchain.json`.
- The generated file embeds exact manifest bytes and manifest SHA.
- The generator validates the manifest and writes deterministic Go code.
Possible generator locations:
    internal/toolchain/generate.go
or:
    internal/cmd/gentoolchain/main.go
Expose:
    toolchain.BundledManifest() toolchain.Manifest
    toolchain.BundledManifestBytes() []byte
    toolchain.BundledManifestSHA256() string
Update:
    scenery version --json
to include:
    {
      "toolchain_manifest": {
        "schema_version": "scenery.toolchain.v1",
        "sha256": "...",
        "artifact_count": 7,
        "source_lock_count": 2
      }
    }
This makes every Scenery release auditable: the binary, root manifest, and checked-in source contract line up.
### Milestone 4: Add `scenery toolchain` CLI
Add:
    cmd/scenery/toolchain.go
    cmd/scenery/toolchain_test.go
CLI shape:
    scenery toolchain list [--json] [--include-source-locks]
    scenery toolchain sync [--json] [--all] [--tool <name>] [--platform <goos/goarch>] [--images]
    scenery toolchain verify [--json] [--all] [--tool <name>] [--platform <goos/goarch>] [--images] [--strict]
    scenery toolchain path [--json] --tool <name> [--platform <goos/goarch>]
Behavior:
- `list` reports bundled manifest entries and local install status.
- `sync` downloads missing or invalid managed binaries.
- `sync --images` verifies or pulls manifest-declared Docker images.
- `verify` checks local files without downloading.
- `verify --strict` fails on missing checksums, tag-only image refs, or any artifact with incomplete metadata.
- `path` prints the exact Scenery-managed binary path for a tool.
- `--platform` defaults to current `runtime.GOOS/runtime.GOARCH`.
- `SCENERY_TOOLCHAIN_DIR` overrides the store root.
- `SCENERY_TOOLCHAIN_DOWNLOAD=0` disables automatic downloads.
- Existing per-tool download-disable env vars should either be routed through this resolver or explicitly deprecated in docs if they are redundant.
JSON status shape:
    {
      "schema_version": "scenery.toolchain.status.v1",
      "manifest_sha256": "...",
      "store_dir": "/repo/.scenery/toolchain",
      "platform": "darwin/arm64",
      "source_locks": [
        {
          "name": "go-modules",
          "kind": "go-modules",
          "manifest": "go.mod",
          "lock": "go.sum",
          "status": "present"
        }
      ],
      "artifacts": [
        {
          "name": "grafana",
          "kind": "binary",
          "version": "13.0.1+security-01",
          "status": "installed",
          "source": "managed-store",
          "managed_path": ".../.scenery/toolchain/artifacts/grafana/13.0.1+security-01/darwin-arm64/bin/grafana"
        }
      ]
    }
### Milestone 5: Replace implicit binary resolution in Grafana and Victoria
Update:
    cmd/scenery/victoria.go
    cmd/scenery/grafana.go
Resolution order for managed tools must be:
1. explicit per-tool env override, such as `SCENERY_GRAFANA_BIN`;
2. managed toolchain store path;
3. automatic manifest-driven download into the managed store, unless disabled;
4. clear error telling the user which `scenery toolchain sync ...` command to run.
Forbidden for managed tools:
    exec.LookPath("grafana")
    exec.LookPath("grafana-server")
    exec.LookPath("victoria-metrics")
    exec.LookPath("victoria-metrics-prod")
    exec.LookPath("victoria-logs")
    exec.LookPath("victoria-logs-prod")
    exec.LookPath("victoria-traces")
    exec.LookPath("victoria-traces-prod")
Allowed:
- explicit env override pointing to a binary;
- managed store binary path;
- explicit external service URL where documented;
- manifest-pinned Docker image ref where Docker mode is selected.
Keep the existing Grafana and Victoria startup behavior where possible, but change the binary source. The code should report the selected source in structured dev events where practical:
    source: "explicit-env"
    source: "managed-store"
    source: "downloaded"
    source: "external-service"
Do not silently reuse a process that happens to be on the expected port unless the existing external-service reuse path verifies compatibility and the docs explain it.
### Milestone 6: Audit legacy async runtime, Postgres, and generated worker tooling
Run:
    rg 'exec\.LookPath|execLookPath|LookPath|docker|postgres|initdb|legacy-async-runtime|bun|npm|node|tsx' cmd internal
Classify every hit:
1. Scenery-managed toolchain artifact.
2. Explicit user-provided external tool.
3. Source package-manager command.
4. Test-only fake.
5. Not relevant.
For each Scenery-managed artifact:
- add it to `scenery.toolchain.json`;
- use `internal/toolchain` for resolution;
- remove implicit `PATH` fallback;
- add tests for fake `PATH` poisoning.
For package-manager/runtime commands such as `bun`, `npm`, `node`, or `tsx`, decide explicitly:
- If Scenery only invokes the project’s chosen package manager, document it as a source dependency, not a Scenery-managed toolchain artifact.
- If Scenery downloads or installs a hidden runtime, put it in the toolchain manifest.
legacy async runtime local dev server uses the managed `legacy-async-runtime-cli` toolchain artifact. Do not add `SCENERY_LEGACY_ASYNC_RUNTIME_BIN` back as a hidden substrate override.
Postgres local startup uses the manifest-pinned Docker image unless an explicit admin URL or external database mode is configured. Do not add local `initdb`/`postgres` binary env overrides back as hidden substrate controls.
Managed service startup may use a local binary or Docker image. If Scenery owns startup, those binaries/images must be manifest-controlled or explicit. No implicit image tag or implicit `PATH`.
### Milestone 7: Add Docker image control
Extend the toolchain manifest for images:
    {
      "name": "postgres",
      "kind": "image",
      "version": "18",
      "images": [
        {
          "ref": "postgres:18",
          "digest": "sha256:...",
          "usage": "dev.services.postgres",
          "optional": true
        }
      ]
    }
Runtime rules:
- If the manifest has a digest, prefer digest-pinned pull/run refs.
- If only a tag is present during migration, mark the image metadata as `unstable`.
- `scenery toolchain verify --strict --images` fails on tag-only image refs.
- `scenery toolchain list --json --images` reports whether images are locally present.
- `scenery toolchain sync --images` pulls missing images when Docker is available.
- If Docker is unavailable and the image is optional, report `unavailable` with a clear message.
- If Docker is required for a selected dev-service mode, fail before startup with the manifest artifact name and expected image ref.
- Docker execution must go through an injectable runner so unit tests do not need Docker.
### Milestone 8: Remove or demote `internal/devtools/versions.json`
There must be only one canonical version source.
Preferred outcome:
- Delete `internal/devtools/versions.json`.
- Replace `internal/devtools.PinnedVersions()` with a small compatibility adapter over `internal/toolchain`.
- Remove the adapter once all callers use `internal/toolchain` directly.
Acceptable temporary outcome:
- Keep `internal/devtools/versions.json` only as generated compatibility output from `scenery.toolchain.json`.
- Add a test proving it is generated and not hand-edited.
Forbidden outcome:
- Two independent manually maintained version files.
### Milestone 9: Update docs, schemas, and agent guidance
Update:
    docs/local-contract.md
Add:
- `scenery toolchain list --json`
- `scenery toolchain sync --json`
- `scenery toolchain verify --json`
- `scenery toolchain path --json`
- `.scenery/toolchain/`
- `scenery.toolchain.json`
- `scenery.toolchain.v1`
- `scenery.toolchain.status.v1`
Update:
    docs/environment.md
Add:
- `SCENERY_TOOLCHAIN_DIR`
- `SCENERY_TOOLCHAIN_DOWNLOAD`
- explicit per-tool binary override vars
- statement that managed toolchain artifacts do not use implicit system `PATH`
Update:
    docs/agent-guide.md
Add agent guidance:
- run `scenery toolchain list --json` to inspect required tools;
- run `scenery toolchain sync --json` before local dev if managed tools are missing;
- do not install global Grafana/Victoria/legacy async runtime/Postgres binaries as a hidden fix;
- prefer explicit env override or managed toolchain store.
Update:
    SKILL.md
Add target-app guidance:
- Scenery-managed tools live under `.scenery/toolchain/`.
- Agents should not rely on system binaries.
- Use `scenery toolchain verify --json` when diagnosing local dev tool issues.
Update:
    README.md
Add a concise human-facing section explaining:
- root frozen toolchain manifest;
- local controlled toolchain store;
- how to sync;
- how to override store location.
Update:
    docs/knowledge.json
Add or update indexed documentation entries for the new toolchain docs.
Add:
    docs/schemas/scenery.toolchain.v1.schema.json
    docs/schemas/scenery.toolchain.status.v1.schema.json
### Milestone 10: Tests and harness
Add tests for `internal/toolchain`:
- parses a valid manifest;
- rejects unknown fields;
- rejects missing versions;
- rejects invalid platform keys;
- rejects missing URLs;
- rejects missing checksums for enforced downloads;
- computes stable manifest SHA;
- resolves current platform;
- computes deterministic store paths;
- verifies installed artifacts;
- handles partial temp install recovery;
- rejects archive path traversal;
- preserves executable bits;
- reports missing artifacts without downloading during verify.
Add CLI tests for `cmd/scenery`:
- `scenery toolchain list --json` emits the expected schema;
- `scenery toolchain verify --json` reports missing artifacts without downloading;
- `scenery toolchain sync --json` uses a fake HTTP server and fake archive;
- `scenery toolchain path --tool <name> --json` reports managed path;
- `SCENERY_TOOLCHAIN_DIR` changes the store root;
- `SCENERY_TOOLCHAIN_DOWNLOAD=0` disables automatic downloads.
Add resolver integration tests:
- Put fake `grafana` and `victoria-metrics` executables earlier in `PATH`.
- Ensure managed resolver ignores them.
- Ensure explicit env override still wins.
- Ensure missing managed binary triggers download or clear failure.
- Ensure structured status reports the source.
Add Docker tests:
- Use a fake Docker runner interface.
- Verify digest-pinned refs are preferred.
- Verify optional images degrade cleanly when Docker is unavailable.
- Verify `--strict` fails on tag-only image refs.
Add docs/harness tests:
- update any schema index validation;
- update `scenery harness self --json --write` expected docs knowledge if needed;
- ensure `PLANS.md` required sections are satisfied.
## Plan of Work
Start by creating the root manifest and schema. Do not change runtime behavior in the first step. This gives tests a stable contract and allows review of naming, metadata shape, and completeness.
Next, build `internal/toolchain` as a standalone package. It should not know about Grafana, Victoria, legacy async runtime, or Postgres specifically. It should only know how to parse a manifest, resolve a platform artifact, manage a local store, verify files, download archives, and report status.
Then add `scenery toolchain` CLI commands. This gives contributors and agents an explicit inspection and sync surface before runtime commands start relying on it.
Then migrate Grafana and Victoria. They are the safest first users because Scenery already has pinned versions and download logic for them. Replace their bespoke version and binary resolution with `internal/toolchain`, while preserving startup behavior, explicit env overrides, and structured dev events.
Then audit legacy async runtime, Postgres, and generated worker tooling. Each implicit binary/image dependency must become manifest-managed, explicit-only, or documented as a source package-manager dependency.
Then update docs, schemas, and knowledge index. Scenery treats machine-readable contracts and docs as part of the implementation.
Finally, run focused tests, full tests, binary install, CLI smoke checks, and self-harness validation.
## Concrete Steps
From the Scenery repository root:
    cd /path/to/scenery
Create the ExecPlan and link it:
    $EDITOR docs/plans/0057-frozen-toolchain-manifest.md
    $EDITOR docs/plans/active.md
Inspect current docs and contracts:
    scenery inspect docs --json
Add the root manifest:
    $EDITOR scenery.toolchain.json
Add schemas:
    $EDITOR docs/schemas/scenery.toolchain.v1.schema.json
    $EDITOR docs/schemas/scenery.toolchain.status.v1.schema.json
Create the internal package:
    mkdir -p internal/toolchain
    $EDITOR internal/toolchain/manifest.go
    $EDITOR internal/toolchain/platform.go
    $EDITOR internal/toolchain/store.go
    $EDITOR internal/toolchain/download.go
    $EDITOR internal/toolchain/archive.go
    $EDITOR internal/toolchain/docker.go
    $EDITOR internal/toolchain/status.go
    $EDITOR internal/toolchain/manifest_test.go
    $EDITOR internal/toolchain/store_test.go
    $EDITOR internal/toolchain/download_test.go
    $EDITOR internal/toolchain/archive_test.go
    $EDITOR internal/toolchain/docker_test.go
Add manifest embedding or generation:
    $EDITOR internal/toolchain/manifest_gen.go
    $EDITOR internal/toolchain/generate.go
or:
    mkdir -p internal/cmd/gentoolchain
    $EDITOR internal/cmd/gentoolchain/main.go
Run generation if applicable:
    go generate ./internal/toolchain
Add CLI:
    $EDITOR cmd/scenery/toolchain.go
    $EDITOR cmd/scenery/toolchain_test.go
Migrate or remove old internal devtool pins:
    $EDITOR internal/devtools/versions.go
    $EDITOR internal/devtools/versions.json
The target state is no second hand-maintained canonical version file.
Migrate Victoria:
    $EDITOR cmd/scenery/victoria.go
    $EDITOR cmd/scenery/victoria_test.go
Migrate Grafana:
    $EDITOR cmd/scenery/grafana.go
    $EDITOR cmd/scenery/grafana_test.go
Audit remaining implicit resolution:
    rg 'exec\.LookPath|execLookPath|LookPath|docker|postgres|initdb|legacy-async-runtime|bun|npm|node|tsx' cmd internal
Update each Scenery-owned artifact to use `internal/toolchain`.
Update docs:
    $EDITOR docs/local-contract.md
    $EDITOR docs/environment.md
    $EDITOR docs/agent-guide.md
    $EDITOR SKILL.md
    $EDITOR README.md
    $EDITOR docs/knowledge.json
Format:
    gofmt -w internal/toolchain internal/devtools cmd/scenery
Run focused tests:
    go test ./internal/toolchain ./internal/devtools ./cmd/scenery
Run broader tests:
    go test ./...
Install the binary:
    go install ./cmd/scenery
Run toolchain smoke checks:
    scenery toolchain list --json
    scenery toolchain verify --json
Use an isolated store to prove sync behavior:
    tmp="$(mktemp -d)"
    SCENERY_TOOLCHAIN_DIR="$tmp" scenery toolchain sync --tool victoria-metrics --json
    SCENERY_TOOLCHAIN_DIR="$tmp" scenery toolchain verify --tool victoria-metrics --json
Use a poisoned `PATH` to prove managed tools do not resolve from system binaries:
    mkdir -p /tmp/scenery-fake-path
    printf '#!/bin/sh\necho fake >&2\nexit 99\n' >/tmp/scenery-fake-path/grafana
    chmod +x /tmp/scenery-fake-path/grafana
    PATH="/tmp/scenery-fake-path:$PATH" SCENERY_TOOLCHAIN_DIR="$(mktemp -d)" scenery toolchain sync --tool grafana --json
Run self-harness:
    scenery harness self --json --write
Check diff hygiene:
    git diff --check
    git status --short
## Validation and Acceptance
Acceptance criteria:
1. `scenery.toolchain.json` exists at the repository root.
2. `scenery.toolchain.json` validates against `docs/schemas/scenery.toolchain.v1.schema.json`.
3. `scenery toolchain list --json` reports:
   - schema version;
   - manifest SHA-256;
   - store directory;
   - platform;
   - managed artifacts;
   - source locks;
   - local install status.
4. `scenery toolchain sync --tool <name> --json` downloads the selected artifact into `.scenery/toolchain/` or `SCENERY_TOOLCHAIN_DIR`.
5. Changing an artifact version in `scenery.toolchain.json` changes the computed install path.
6. The next sync after a version bump downloads the new version instead of reusing the old binary.
7. Managed Grafana and Victoria startup no longer call `exec.LookPath` for implicit system binary resolution.
8. Explicit env override still works and status reports `source: "explicit-env"`.
9. A fake binary earlier in `PATH` is ignored by managed resolver tests.
10. Downloads verify SHA-256 before extraction.
11. Extracted archives cannot write outside the intended destination.
12. Docker images used by Scenery-owned dev services are declared in `scenery.toolchain.json`.
13. `scenery toolchain verify --strict --images` fails on tag-only image refs unless the plan explicitly leaves a temporary migration exception with follow-up work.
14. `scenery version --json` includes toolchain manifest metadata.
15. Documentation mentions:
    - `scenery.toolchain.json`;
    - `.scenery/toolchain/`;
    - `SCENERY_TOOLCHAIN_DIR`;
    - `SCENERY_TOOLCHAIN_DOWNLOAD`;
    - no implicit `PATH` for managed tools.
Required validation commands:
    go test ./internal/toolchain ./internal/devtools ./cmd/scenery
    go test ./...
    go install ./cmd/scenery
    scenery toolchain list --json
    scenery toolchain verify --json
    scenery harness self --json --write
    git diff --check
## Idempotence and Recovery
`scenery toolchain sync` must be idempotent. Running it repeatedly with the same manifest should not redownload artifacts that are already installed and checksum-valid.
Partial downloads and extractions must use temporary paths. A failed download, checksum mismatch, interrupted extraction, or process kill must not leave a final-looking install directory.
The next run should clean or ignore stale temp paths.
A corrupted installed binary should be detected by:
    scenery toolchain verify --json
A corrupted installed binary should be replaced by:
    scenery toolchain sync --json
A version bump must not mutate old version directories. Old directories may remain until a later cleanup command exists.
Docker image sync should be safe to rerun. Missing Docker should produce structured `unavailable` status for optional images and clear failure for required images.
Explicit env override recovery is manual. If `SCENERY_GRAFANA_BIN` points to a missing or non-executable file, Scenery should fail with that path and not fall back to `PATH`.
## Artifacts and Notes
Example local status:
    {
      "schema_version": "scenery.toolchain.status.v1",
      "manifest_sha256": "8f...",
      "store_dir": "/repo/.scenery/toolchain",
      "platform": "darwin/arm64",
      "source_locks": [
        {
          "name": "go-modules",
          "kind": "go-modules",
          "manifest": "go.mod",
          "lock": "go.sum",
          "status": "present"
        }
      ],
      "artifacts": [
        {
          "name": "victoria-metrics",
          "kind": "binary",
          "version": "v1.141.0",
          "status": "installed",
          "source": "managed-store",
          "managed_path": "/repo/.scenery/toolchain/artifacts/victoria-metrics/v1.141.0/darwin-arm64/bin/victoria-metrics-prod"
        }
      ]
    }
Example failure when downloads are disabled:
    scenery: toolchain artifact victoria-metrics v1.141.0 for darwin/arm64 is not installed under .scenery/toolchain
    scenery: automatic downloads are disabled by SCENERY_TOOLCHAIN_DOWNLOAD=0
    scenery: run `scenery toolchain sync --tool victoria-metrics` or set SCENERY_VICTORIA_METRICS_BIN to an explicit binary
Example failure when an implicit system binary exists but no managed binary is installed:
    scenery: managed toolchain artifact grafana is not installed
    scenery: system PATH binaries are not used for managed toolchain artifacts
    scenery: run `scenery toolchain sync --tool grafana` or set SCENERY_GRAFANA_BIN explicitly
The exact wording may change, but the behavior must not.
## Interfaces and Dependencies
New interfaces:
- `scenery.toolchain.json`
  - Root checked-in manifest.
  - Freezes Scenery-owned managed artifacts and source lock references for the current source/release version.
- `docs/schemas/scenery.toolchain.v1.schema.json`
  - Schema for the root manifest.
- `docs/schemas/scenery.toolchain.status.v1.schema.json`
  - Schema for CLI status output if this output is declared stable or beta.
- `internal/toolchain`
  - Internal parser, resolver, downloader, verifier, Docker image checker, and status package.
- `scenery toolchain list --json`
  - Machine-readable inventory and local status.
- `scenery toolchain sync --json`
  - Managed artifact installer and optional image puller.
- `scenery toolchain verify --json`
  - Local toolchain verification.
- `scenery toolchain path --json --tool <name>`
  - Exact managed path lookup.
- `.scenery/toolchain/`
  - Default local toolchain store.
  - Ignored local state.
- `SCENERY_TOOLCHAIN_DIR`
  - Override for the toolchain store root.
- `SCENERY_TOOLCHAIN_DOWNLOAD`
  - Global control for automatic downloads.
Existing interfaces to preserve deliberately:
- Explicit per-tool binary overrides, such as `SCENERY_GRAFANA_BIN` and Victoria component `*_BIN` variables.
- Existing Grafana/Victoria enable/disable controls, routed through the new resolver where relevant.
- Existing external-service URL controls, where they represent deliberate external reuse rather than implicit local discovery.
Interfaces to remove or forbid for managed toolchain artifacts:
- Implicit `exec.LookPath` fallback.
- Implicit unpinned Docker image tags.
- Unversioned install locations that allow a version bump to accidentally reuse an old binary.
- Two independent hand-maintained version manifests.
Source lock policy:
- Go module dependencies stay frozen by `go.mod` and `go.sum`.
- UI/package-manager dependencies stay frozen by their package lock files.
- `scenery.toolchain.json` references these lock files and reports them through `scenery toolchain list --json`.
- `scenery.toolchain.json` does not duplicate full Go or package-manager dependency graphs unless a later plan adds generated lock summaries.

The key naming choices are now:

scenery.toolchain.json
.scenery/toolchain/
SCENERY_TOOLCHAIN_DIR
SCENERY_TOOLCHAIN_DOWNLOAD
scenery toolchain list
scenery toolchain sync
scenery toolchain verify
scenery toolchain path
