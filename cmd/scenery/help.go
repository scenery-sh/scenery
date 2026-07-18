package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	helpManifestKind = "scenery.help"
)

type helpRootGroup struct {
	Name    string
	Entries []helpRootEntry
}

type helpRootEntry struct {
	Command string
	Summary string
}

type helpReferenceGroup struct {
	Name     string
	Commands []string
}

type helpCommandEntry struct {
	Command     string   `json:"command"`
	Group       string   `json:"group"`
	Summary     string   `json:"summary"`
	Usage       []string `json:"usage"`
	Subcommands []string `json:"subcommands,omitempty"`
	Flags       []string `json:"flags,omitempty"`
	Notes       []string `json:"notes,omitempty"`
	JSON        bool     `json:"json"`
	Stability   string   `json:"stability"`
}

type helpManifest struct {
	cliPayloadIdentity
	Commands []helpCommandEntry `json:"commands"`
}

var rootHelpGroups = []helpRootGroup{
	{Name: "Local dev", Entries: []helpRootEntry{
		{Command: "up", Summary: "Start a local dev runtime"},
		{Command: "ps", Summary: "Show local dev app roots"},
		{Command: "logs", Summary: "Read, follow, or query dev logs"},
		{Command: "console", Summary: "Open the source-aware dev console"},
		{Command: "down", Summary: "Stop a local dev runtime"},
		{Command: "prune", Summary: "Remove old stopped dev state"},
	}},
	{Name: "Build and runtime", Entries: []helpRootEntry{
		{Command: "worker", Summary: "Run app workers"},
		{Command: "build", Summary: "Build the deployable binary"},
		{Command: "check", Summary: "Check the app model"},
		{Command: "test", Summary: "Run Go tests"},
	}},
	{Name: "App resources", Entries: []helpRootEntry{
		{Command: "inspect", Summary: "Inspect app model and diagnostics as JSON"},
		{Command: "compile", Summary: "Compile the canonical app manifest"},
		{Command: "list", Summary: "List app resources"},
		{Command: "get", Summary: "Get one app resource"},
		{Command: "explain", Summary: "Explain a resource and provenance"},
		{Command: "schema", Summary: "Read a resource schema"},
		{Command: "fmt", Summary: "Format Scenery source"},
		{Command: "generate", Summary: "Generate clients, SQLC, and configured outputs"},
		{Command: "db", Summary: "Manage Postgres database lifecycle"},
		{Command: "task", Summary: "List, inspect, graph, and run app tasks"},
		{Command: "validate", Summary: "Run validation profiles"},
		{Command: "storage", Summary: "Inspect configured storage"},
		{Command: "snapshot", Summary: "Save and load portable app snapshots"},
		{Command: "symphony", Summary: "Manage local Symphony workflow mode"},
		{Command: "harness", Summary: "Run Scenery harnesses"},
	}},
	{Name: "Workspace", Entries: []helpRootEntry{
		{Command: "worktree", Summary: "Create, list, and remove app worktrees"},
	}},
	{Name: "Observability", Entries: []helpRootEntry{
		{Command: "traces", Summary: "List or clear local traces"},
		{Command: "metrics", Summary: "List, query, and inspect local metrics"},
	}},
	{Name: "System", Entries: []helpRootEntry{
		{Command: "doctor", Summary: "Check host and app readiness"},
		{Command: "deploy", Summary: "Manage public deploy intent"},
		{Command: "version", Summary: "Print version information"},
		{Command: "upgrade", Summary: "Upgrade the local Scenery binary"},
		{Command: "system", Summary: "Manage agent, edge, trust, and toolchain"},
	}},
}

var helpReferenceGroups = []helpReferenceGroup{
	{Name: "App contract", Commands: []string{
		"scenery fmt",
		"scenery compile",
		"scenery schema",
		"scenery list",
		"scenery get",
		"scenery explain",
	}},
	{Name: "Local dev", Commands: []string{
		"scenery up",
		"scenery ps",
		"scenery logs",
		"scenery logs query",
		"scenery logs tail",
		"scenery console",
		"scenery down",
		"scenery prune",
	}},
	{Name: "Database", Commands: []string{
		"scenery db list",
		"scenery db shell",
		"scenery db apply",
		"scenery db seed",
		"scenery db setup",
		"scenery db reset",
		"scenery db drop",
	}},
	{Name: "Workspace", Commands: []string{
		"scenery worktree create",
		"scenery worktree list",
		"scenery worktree remove",
	}},
	{Name: "Generation", Commands: []string{
		"scenery generate",
		"scenery generate sqlc",
	}},
	{Name: "Tasks", Commands: []string{
		"scenery task list",
		"scenery task inspect",
		"scenery task run",
		"scenery task graph",
	}},
	{Name: "Validation", Commands: []string{
		"scenery validate",
		"scenery validate list",
		"scenery validate inspect",
		"scenery validate graph",
		"scenery validate changed",
	}},
	{Name: "Storage", Commands: []string{
		"scenery snapshot save",
		"scenery snapshot verify",
		"scenery snapshot load",
		"scenery storage status",
		"scenery storage webui",
		"scenery storage ls",
		"scenery storage stat",
		"scenery storage put",
		"scenery storage get",
		"scenery storage rm",
	}},
	{Name: "Symphony", Commands: []string{
		"scenery symphony auto",
	}},
	{Name: "Runtime", Commands: []string{
		"scenery worker",
		"scenery worker durable",
		"scenery worker durable jobs",
		"scenery worker durable token create",
	}},
	{Name: "Build and checks", Commands: []string{
		"scenery build",
		"scenery check",
		"scenery test",
	}},
	{Name: "Harness", Commands: []string{
		"scenery harness",
		"scenery harness self",
		"scenery harness ui",
	}},
	{Name: "Inspection", Commands: []string{
		"scenery inspect app",
		"scenery inspect routes",
		"scenery inspect services",
		"scenery inspect endpoints",
		"scenery inspect build",
		"scenery inspect paths",
		"scenery inspect generators",
		"scenery inspect durable",
		"scenery inspect storage",
		"scenery inspect observability",
		"scenery inspect validation",
		"scenery inspect docs",
		"scenery inspect harness artifact",
		"scenery inspect harness diagnostics",
		"scenery inspect harness timing",
	}},
	{Name: "Observability", Commands: []string{
		"scenery traces list",
		"scenery traces clear",
		"scenery metrics list",
		"scenery metrics query",
		"scenery metrics labels",
		"scenery metrics series",
	}},
	{Name: "System", Commands: []string{
		"scenery doctor",
		"scenery deploy",
		"scenery version",
		"scenery upgrade",
		"scenery system agent",
		"scenery system agent restart",
		"scenery system edge",
		"scenery system toolchain",
		"scenery system trust",
	}},
}

var helpCommands = []helpCommandEntry{
	{Command: "fmt", Group: "App contract", Summary: "Format .scn source.", Usage: []string{"scenery fmt [--check] [--app-root <path>] [-o human|json]"}, Flags: []string{"--check", "--app-root <path>", "-o human|json"}, JSON: true, Stability: "stable"},
	{Command: "compile", Group: "App contract", Summary: "Compile the canonical app manifest.", Usage: []string{"scenery compile [--view source|effective|expanded] [--app-root <path>] [-o human|json]"}, Flags: []string{"--view <view>", "--app-root <path>", "-o human|json"}, JSON: true, Stability: "stable"},
	{Command: "schema", Group: "App contract", Summary: "Read a resource schema.", Usage: []string{"scenery schema <kind> [-o human|json]"}, Flags: []string{"-o human|json"}, JSON: true, Stability: "stable"},
	{Command: "list", Group: "App contract", Summary: "List canonical resources by kind.", Usage: []string{"scenery list <kind> [--module <name>] [-o human|json]"}, Flags: []string{"--module <name>", "-o human|json"}, JSON: true, Stability: "stable"},
	{Command: "get", Group: "App contract", Summary: "Get one canonical resource.", Usage: []string{"scenery get <address> [-o human|json]"}, Flags: []string{"-o human|json"}, JSON: true, Stability: "stable"},
	{Command: "explain", Group: "App contract", Summary: "Explain a canonical resource and provenance.", Usage: []string{"scenery explain <address> [-o human|json]"}, Flags: []string{"-o human|json"}, JSON: true, Stability: "stable"},
	{
		Command:   "up",
		Group:     "Local dev",
		Summary:   "Start a supervised local dev runtime.",
		Usage:     []string{"scenery up [--env <name>] [--port <n>] [--listen <addr>] [--app-root <path>] [--claim-aliases] [--verbose] [-o jsonl] [--detach] [--wait=ready|registered]"},
		Flags:     []string{"--env <name>", "--port <n>", "--listen <addr>", "--app-root <path>", "--claim-aliases", "--verbose", "-o", "json", "--detach", "--wait=ready|registered"},
		JSON:      true,
		Stability: "stable",
	},
	{
		Command:   "ps",
		Group:     "Local dev",
		Summary:   "Show local dev app roots.",
		Usage:     []string{"scenery ps [-o json] [--app-root <path>] [--watch]"},
		Flags:     []string{"-o", "json", "--app-root <path>", "--watch"},
		Notes:     []string{"Human table output is the default and shows console URLs.", "`-o json` emits the current scenery.agent.status payload for agents and automation."},
		JSON:      true,
		Stability: "stable",
	},
	{
		Command:     "logs",
		Group:       "Local dev",
		Summary:     "Read, follow, or query dev logs.",
		Usage:       []string{"scenery logs [flags]", "scenery logs query --query <logsql> [flags]", "scenery logs tail --query <logsql> [flags]"},
		Subcommands: []string{"query", "tail"},
		Flags: []string{
			"--app-root <path>", "--since <duration>",
			"--limit <n>", "--fields <csv>", "-o", "json", "-o", "jsonl",
			"--stream all|stdout|stderr", "--source <id>", "--kind <kind>",
			"--level <level>", "--grep <text>", "--follow",
		},
		JSON:      true,
		Stability: "stable",
	},
	{
		Command:   "console",
		Group:     "Local dev",
		Summary:   "Open the source-aware dev console.",
		Usage:     []string{"scenery console [--app-root <path>] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>]"},
		Flags:     []string{"--app-root <path>", "--source <id>", "--kind <kind>", "--level <level>", "--grep <text>", "--since <duration>"},
		Stability: "stable",
	},
	{
		Command:   "down",
		Group:     "Local dev",
		Summary:   "Stop a local dev runtime.",
		Usage:     []string{"scenery down [--app-root <path>] [--db] [--state] [--all] [-o json]"},
		Flags:     []string{"--app-root <path>", "--db", "--state", "--all", "-o", "json"},
		JSON:      true,
		Stability: "stable",
	},
	{
		Command:   "prune",
		Group:     "Local dev",
		Summary:   "Remove old stopped session state.",
		Usage:     []string{"scenery prune --older-than <duration> [--app-root <path>] [-o json]"},
		Flags:     []string{"--older-than <duration>", "--app-root <path>", "-o", "json"},
		JSON:      true,
		Stability: "stable",
	},
	{
		Command: "db",
		Group:   "Database",
		Summary: "Manage Postgres database lifecycle, server status, and local shells.",
		Usage: []string{
			"scenery db list|shell [--app-root <path>] [service]",
			"scenery db apply|seed|setup|reset|drop [--app-root <path>] [-o json]",
			"scenery db server status|start|stop|logs [-o json] [--yes]",
		},
		Subcommands: []string{"list", "shell", "apply", "seed", "setup", "reset", "drop", "server"},
		Flags:       []string{"--app-root <path>", "-o", "json", "--dry-run", "--yes"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command:     "worktree",
		Group:       "Workspace",
		Summary:     "Create, list, and remove app worktrees.",
		Usage:       []string{"scenery worktree create <name> [--from <branch>] [--app-root <path>] [-o json]", "scenery worktree list [--app-root <path>] [-o json]", "scenery worktree remove <name> [--app-root <path>] [--db] [-o json]"},
		Subcommands: []string{"create", "list", "remove"},
		Flags:       []string{"--from <branch>", "--app-root <path>", "--db", "-o", "json"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command:     "generate",
		Group:       "Generation",
		Summary:     "Generate native contracts, TypeScript targets, SQLC, and configured outputs.",
		Usage:       []string{"scenery generate [--target contracts|typescript_client.<name>] [--check] [--app-root <path>] [-o human|json]", "scenery generate [--app-root <path>] [--dry-run] [-o json]", "scenery generate sqlc [--app-root <path>] [--dry-run] [-o json]"},
		Subcommands: []string{"sqlc"},
		Flags:       []string{"--target <target>", "--check", "--app-root <path>", "--dry-run", "-o", "json", "-o human|json"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command:     "task",
		Group:       "Tasks",
		Summary:     "List, inspect, graph, and run app tasks.",
		Usage:       []string{"scenery task list [--app-root <path>] [-o json]", "scenery task inspect <target> [--app-root <path>] [--lang go|typescript] [-o json]", "scenery task run <target> [--app-root <path>] [--env <name>] [--lang go|typescript] [-- script args...]", "scenery task graph -o json [--app-root <path>]"},
		Subcommands: []string{"list", "inspect", "run", "graph"},
		Flags:       []string{"--app-root <path>", "--env <name>", "--lang go|typescript", "-o", "json"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command: "storage",
		Group:   "App resources",
		Summary: "Inspect beta local-dev storage capability state.",
		Usage: []string{
			"scenery storage status -o json [--app-root <path>]",
			"scenery storage webui -o json [--app-root <path>]",
			"scenery storage ls <store> [--prefix <prefix>] [--cursor <cursor>] [--limit <n>] -o json [--app-root <path>]",
			"scenery storage stat <store> <key> -o json [--app-root <path>]",
			"scenery storage put <store> <key> <file> -o json [--app-root <path>]",
			"scenery storage get <store> <key> --output <file> -o json [--app-root <path>]",
			"scenery storage rm <store> <key> [--recursive] -o json [--app-root <path>]",
			"scenery storage cleanup [--yes] -o json [--app-root <path>]",
		},
		Subcommands: []string{"status", "webui", "ls", "stat", "put", "get", "rm", "cleanup"},
		Flags:       []string{"-o", "json", "--app-root <path>", "--prefix <prefix>", "--cursor <cursor>", "--limit <n>", "--output <file>", "--recursive", "--yes"},
		JSON:        true,
		Stability:   "beta",
	},
	{
		Command:     "snapshot",
		Group:       "Storage",
		Summary:     "Save, verify, and load portable Postgres and storage snapshots.",
		Usage:       []string{"scenery snapshot save --output <file.zip> [--db] [--storage] [--app-root <path>] [-o json]", "scenery snapshot verify --input <file.zip> [-o json]", "scenery snapshot load --input <file.zip> [--db] [--storage] --mode overwrite|merge [--on-conflict fail|skip|overwrite] [--yes] [--dry-run] [--app-root <path>] [-o json]"},
		Subcommands: []string{"save", "load"},
		Flags:       []string{"--output <file.zip>", "--input <file.zip>", "--db", "--storage", "--mode overwrite|merge", "--on-conflict fail|skip|overwrite", "--yes", "--dry-run", "--app-root <path>", "-o", "json"},
		JSON:        true,
		Stability:   "beta",
	},
	{
		Command:     "symphony",
		Group:       "App resources",
		Summary:     "Manage local Symphony workflow mode.",
		Usage:       []string{"scenery symphony auto --on|--off [--app-root <path>]"},
		Subcommands: []string{"auto"},
		Flags:       []string{"--on", "--off", "--app-root <path>"},
		Stability:   "beta",
	},
	{
		Command:     "validate",
		Group:       "Validation",
		Summary:     "Run app-owned validation profiles.",
		Usage:       []string{"scenery validate [<profile>] [--app-root <path>] [-o json] [--write] [--dry-run]", "scenery validate list [--app-root <path>] [-o json]", "scenery validate inspect <profile> [--app-root <path>] [-o json]", "scenery validate graph [<profile>] [--app-root <path>] -o json", "scenery validate changed [--base <ref>] [--app-root <path>] [-o json] [--write] [--dry-run]"},
		Subcommands: []string{"list", "inspect", "graph", "changed"},
		Flags:       []string{"--app-root <path>", "--base <ref>", "-o", "json", "--write", "--dry-run"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command: "worker",
		Group:   "Runtime",
		Summary: "Run workers.",
		Usage: []string{
			"scenery worker [--app-root <path>] [--env <name>] [--log-format text|json]",
			"scenery worker durable --endpoint <url> --token <token> [--service <name>]... [--app-root <path>] [--env <name>] [--log-format text|json]",
			"scenery worker durable jobs list|inspect|cancel|retry [job-id] --service <name> [--app-root <path>] -o json",
			"scenery worker durable token create --service <name> [--name <name>] [--id <id>] [--app-root <path>] -o json",
		},
		Subcommands: []string{"durable"},
		Flags:       []string{"--app-root <path>", "--env <name>", "--log-format text|json", "-o", "json"},
		JSON:        true,
		Stability:   "beta",
	},
	{
		Command:   "build",
		Group:     "Build and checks",
		Summary: "Build the deployable binary or a declared shared library.",
		Usage: []string{
			"scenery build [--app-root <path>] [--output <path>] [-o human|json]",
			"scenery build --lib <name> [--version <semver>] [--platform all|host|darwin/arm64,linux/amd64] [--app-root <path>] [--output <dir>] [-o human|json]",
		},
		Flags:     []string{"--app-root <path>", "--output <path>", "--lib <name>", "--version <semver>", "--platform <matrix>", "-o human|json"},
		Stability: "stable",
	},
	{
		Command:   "check",
		Group:     "Build and checks",
		Summary:   "Check the app model.",
		Usage:     []string{"scenery check [--app-root <path>] [-o json]"},
		Flags:     []string{"--app-root <path>", "-o", "json"},
		JSON:      true,
		Stability: "stable",
	},
	{
		Command:   "test",
		Group:     "Build and checks",
		Summary:   "Run Go tests for an app.",
		Usage:     []string{"scenery test [--app-root <path>] [go test flags/packages...]"},
		Flags:     []string{"--app-root <path>"},
		Stability: "stable",
	},
	{
		Command:     "harness",
		Group:       "Harness",
		Summary:     "Run framework and UI harnesses.",
		Usage:       []string{"scenery harness [--app-root <path>] [-o json] [--write] [--with-validation[=<profile>]]", "scenery harness self [--repo-root <path>] [--summary] [-o human|json] [--write] [--quick|--race|--release] [--fresh-tests]", "scenery harness ui -o json [--app-root <path>] [--dashboard-url <url>] [--headed] [--write]"},
		Subcommands: []string{"self", "ui"},
		Flags:       []string{"--app-root <path>", "--repo-root <path>", "--summary", "-o", "json", "--summary", "-o", "json", "-o", "json", "--write", "--quick", "--race", "--release", "--fresh-tests", "--dashboard-url <url>", "--headed", "--with-validation[=<profile>]"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command: "inspect",
		Group:   "Inspection",
		Summary: "Inspect app models, UI adherence, and diagnostics.",
		Usage: []string{
			"scenery inspect app|routes|services|endpoints|build|paths|generators|durable|storage|observability|validation -o json [--app-root <path>]",
			"scenery inspect ui [--frontend <name>] [--app-root <path>] [-o human|json]",
			"scenery inspect docs -o json [--repo-root <path>]",
			"scenery inspect harness [artifact <name>|diagnostics --severity error|warning|timing --top <n>] -o json [--app-root <path>] [--repo-root <path>]",
		},
		Subcommands: []string{"app", "routes", "services", "endpoints", "build", "paths", "generators", "durable", "observability", "validation", "ui", "docs", "harness"},
		Flags:       []string{"-o", "human|json", "--app-root <path>", "--frontend <name>", "--repo-root <path>"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command:     "inspect harness",
		Group:       "Inspection",
		Summary:     "Inspect harness artifacts, diagnostics, and timings.",
		Usage:       []string{"scenery inspect harness [artifact <name>|diagnostics --severity error|warning|timing --top <n>] -o json [--app-root <path>] [--repo-root <path>]"},
		Subcommands: []string{"artifact", "diagnostics", "timing"},
		Flags:       []string{"-o", "json", "--app-root <path>", "--repo-root <path>", "--severity error|warning", "--top <n>"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command:     "traces",
		Group:       "Observability",
		Summary:     "List or clear local traces.",
		Usage:       []string{"scenery traces list [-o json] [--app-root <path>] [--service <name>] [--endpoint <name>] [--trace-id <id>] [--status ok|error] [--min-duration-ms <n>] [--since <duration>] [--limit <n>] [--slowest]", "scenery traces clear -o json [--app-root <path>]"},
		Subcommands: []string{"list", "clear"},
		Flags:       []string{"-o", "json", "--app-root <path>", "--service <name>", "--endpoint <name>", "--trace-id <id>", "--status ok|error", "--min-duration-ms <n>", "--since <duration>", "--limit <n>", "--slowest"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command:     "metrics",
		Group:       "Observability",
		Summary:     "List, query, and inspect local metrics.",
		Usage:       []string{"scenery metrics list [-o json] [--app-root <path>] [--service <name>] [--endpoint <name>] [--status ok|error] [--since <duration>] [--limit <n>]", "scenery metrics query [-o json] [--app-root <path>] --promql <query> [--instant] [--since <duration>] [--start <time>] [--end <time>] [--step <duration>] [--timeout <duration>] [--limit <n>]", "scenery metrics labels|series [-o json] [--app-root <path>] --match <selector> [flags]"},
		Subcommands: []string{"list", "query", "labels", "series"},
		Flags:       []string{"-o", "json", "--app-root <path>", "--promql <query>", "--match <selector>", "--instant", "--since <duration>", "--start <time>", "--end <time>", "--step <duration>", "--timeout <duration>", "--limit <n>"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command:   "doctor",
		Group:     "System",
		Summary:   "Check host and app readiness.",
		Usage:     []string{"scenery doctor [--app-root <path>] [-o json]"},
		Flags:     []string{"--app-root <path>", "-o", "json"},
		JSON:      true,
		Stability: "stable",
	},
	{
		Command:     "deploy",
		Group:       "System",
		Summary:     "Sync an app over SSH or manage public deploy intent.",
		Usage:       []string{"scenery deploy <ssh-target> [--app-root <path>]", "scenery deploy enable [--app-root <path>] [-o json]", "scenery deploy disable [--app-root <path>] [-o json]", "scenery deploy publish [--app-root <path>] [-o json]", "scenery deploy status [-o json]", "scenery deploy setup [--acme-email <email>] [--acme-ca production|staging] [-o json]", "scenery deploy resume [-o json]", "scenery deploy teardown [-o json]"},
		Subcommands: []string{"enable", "disable", "publish", "status", "setup", "resume", "teardown"},
		Flags:       []string{"--app-root <path>", "-o", "json", "--acme-email <email>", "--acme-ca production|staging"},
		Notes:       []string{"The positional SSH target performs beta source sync with brief downtime; apps with a deploy domain and a production frontend also publish that frontend remotely.", "`enable`, `disable`, `publish`, `status`, `setup`, `resume`, and `teardown` manage the local public edge.", "`publish` builds each `serve: \"production\"` frontend, publishes it atomically for direct managed-Caddy serving, reloads the edge, and probes the result.", "`setup` uses launchd on macOS (normal user) and systemd on Linux (root)."},
		JSON:        true,
		Stability:   "beta",
	},
	{
		Command:   "version",
		Group:     "System",
		Summary:   "Print version information.",
		Usage:     []string{"scenery version [-o json]"},
		Flags:     []string{"-o", "json"},
		JSON:      true,
		Stability: "stable",
	},
	{
		Command:   "upgrade",
		Group:     "System",
		Summary:   "Upgrade the local Scenery binary.",
		Usage:     []string{"scenery upgrade [--target <path>] [--toolchain installed|all|none] [--force] [--dry-run] [-o json]"},
		Flags:     []string{"--target <path>", "--toolchain installed|all|none", "--skip-toolchain", "--force", "--dry-run", "-o", "json"},
		Notes:     []string{"Downloads are verified against the release checksums.txt asset.", "`--toolchain installed` syncs managed tools already present in the local store; `all` syncs every manifest artifact and image from the upgraded binary."},
		JSON:      true,
		Stability: "stable",
	},
	{
		Command:     "system",
		Group:       "System",
		Summary:     "Manage agent, edge, trust, and toolchain.",
		Usage:       []string{"scenery system agent [--socket <path>] [--router-listen <addr>] [--router-tls|--router-http] [--trust] [-o json]", "scenery system agent restart [--socket <path>] [--router-listen <addr>] [--router-tls|--router-http] [--trust] [-o json]", "scenery system edge install|trust|status|restart|uninstall|dns|privileged [-o json]", "scenery system toolchain list|sync|verify [-o json] [--tool <name>] [--images]", "scenery system toolchain path [-o json] --tool <name>", "scenery system trust [-o json]"},
		Subcommands: []string{"agent", "edge", "toolchain", "trust"},
		Flags:       []string{"--socket <path>", "--router-listen <addr>", "--router-tls", "--router-http", "--trust", "-o", "json", "--tool <name>", "--images"},
		JSON:        true,
		Stability:   "stable",
	},
}

func helpCommand(args []string) error {
	if len(args) == 0 {
		writeRootHelp(os.Stdout)
		return nil
	}
	if (len(args) == 2 && args[0] == "-o" && args[1] == "json") || (len(args) == 1 && args[0] == "-o=json") {
		return writeHelpJSON(os.Stdout)
	}
	if len(args) == 1 && args[0] == "all" {
		writeHelpAll(os.Stdout)
		return nil
	}
	if len(args) > 0 && strings.HasPrefix(args[0], "-") {
		return fmt.Errorf("unknown help flag %q", args[0])
	}
	entry, ok := findHelpCommand(args)
	if !ok {
		if handled, err := runBindingCLI(os.Stdout, os.Stderr, append(append([]string(nil), args...), "--help")); handled {
			return err
		}
		return fmt.Errorf("unknown help topic %q", strings.Join(args, " "))
	}
	writeCommandHelp(os.Stdout, entry)
	return nil
}

func writeRootHelp(w io.Writer) {
	fmt.Fprintln(w, "Scenery - build, run, and inspect app services.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  scenery <command> [args] [flags]")
	fmt.Fprintln(w, "  scenery help <command>")
	fmt.Fprintln(w, "  scenery help all")
	fmt.Fprintln(w, "  scenery help -o json")
	for _, group := range rootHelpGroups {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "%s:\n", group.Name)
		for _, entry := range group.Entries {
			fmt.Fprintf(w, "  %-10s %s\n", entry.Command, entry.Summary)
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, `Use "scenery help <command>" for flags and subcommands.`)
}

func writeHelpAll(w io.Writer) {
	fmt.Fprintln(w, "Scenery command reference")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  scenery <command> [args] [flags]")
	fmt.Fprintln(w, "  scenery help <command>")
	for _, group := range helpReferenceGroups {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "%s:\n", group.Name)
		for _, command := range group.Commands {
			fmt.Fprintf(w, "  %s\n", command)
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, `Use "scenery help <command>" for exact flags.`)
	fmt.Fprintln(w, `Use "scenery help -o json" for the machine-readable command manifest.`)
}

func writeCommandHelp(w io.Writer, entry helpCommandEntry) {
	fmt.Fprintln(w, entry.Summary)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	for _, usage := range entry.Usage {
		fmt.Fprintf(w, "  %s\n", usage)
	}
	if len(entry.Subcommands) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Commands:")
		for _, subcommand := range entry.Subcommands {
			fmt.Fprintf(w, "  %s\n", subcommand)
		}
	}
	if len(entry.Flags) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Flags:")
		for _, flag := range entry.Flags {
			fmt.Fprintf(w, "  %s\n", flag)
		}
	}
	if len(entry.Notes) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Notes:")
		for _, note := range entry.Notes {
			fmt.Fprintf(w, "  %s\n", note)
		}
	}
}

func writeHelpJSON(w io.Writer) error {
	manifest := helpManifest{
		cliPayloadIdentity: newCLIPayloadIdentity(helpManifestKind),
		Commands:           append([]helpCommandEntry(nil), helpCommands...),
	}
	return writeCLIJSON(w, manifest)
}

func findHelpCommand(parts []string) (helpCommandEntry, bool) {
	topic := strings.TrimSpace(strings.Join(parts, " "))
	for topic != "" {
		for _, entry := range helpCommands {
			if entry.Command == topic {
				return entry, true
			}
		}
		cut := strings.LastIndex(topic, " ")
		if cut < 0 {
			break
		}
		topic = topic[:cut]
	}
	return helpCommandEntry{}, false
}
