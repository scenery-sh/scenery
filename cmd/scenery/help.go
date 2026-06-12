package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

const helpManifestSchemaVersion = "scenery.help.v1"

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
	SchemaVersion string             `json:"schema_version"`
	Commands      []helpCommandEntry `json:"commands"`
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
		{Command: "serve", Summary: "Run the API server once"},
		{Command: "worker", Summary: "Run workers and manage worker deployments"},
		{Command: "build", Summary: "Build the deployable binary"},
		{Command: "check", Summary: "Check the app model"},
		{Command: "test", Summary: "Run Go tests"},
	}},
	{Name: "App resources", Entries: []helpRootEntry{
		{Command: "inspect", Summary: "Inspect app model and diagnostics as JSON"},
		{Command: "generate", Summary: "Generate clients, SQLC, and configured outputs"},
		{Command: "db", Summary: "Manage database lifecycle and branches"},
		{Command: "task", Summary: "List, inspect, graph, and run app tasks"},
		{Command: "validate", Summary: "Run validation profiles"},
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
		{Command: "version", Summary: "Print version information"},
		{Command: "system", Summary: "Manage agent, edge, trust, and toolchain"},
	}},
}

var helpReferenceGroups = []helpReferenceGroup{
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
		"scenery db psql",
		"scenery db apply",
		"scenery db seed",
		"scenery db setup",
		"scenery db reset",
		"scenery db drop",
		"scenery db snapshot create",
		"scenery db snapshot restore",
		"scenery db branch status",
		"scenery db branch list",
		"scenery db branch checkout",
		"scenery db branch reset",
		"scenery db branch delete",
		"scenery db branch restore",
		"scenery db branch diff",
		"scenery db branch expire",
		"scenery db branch prune",
		"scenery db postgres install",
		"scenery db postgres start",
		"scenery db postgres status",
		"scenery db postgres logs",
		"scenery db postgres stop",
		"scenery db postgres restart",
		"scenery db postgres uninstall",
	}},
	{Name: "Workspace", Commands: []string{
		"scenery worktree create",
		"scenery worktree list",
		"scenery worktree remove",
	}},
	{Name: "Generation", Commands: []string{
		"scenery generate",
		"scenery generate client",
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
	{Name: "Runtime", Commands: []string{
		"scenery serve",
		"scenery worker",
		"scenery worker bindings",
		"scenery worker typescript",
		"scenery worker deployment set-current",
		"scenery worker deployment ramp",
		"scenery worker deployment drain",
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
		"scenery inspect wire",
		"scenery inspect build",
		"scenery inspect paths",
		"scenery inspect generators",
		"scenery inspect temporal",
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
		"scenery version",
		"scenery system agent",
		"scenery system agent restart",
		"scenery system edge",
		"scenery system toolchain",
		"scenery system trust",
	}},
}

var helpCommands = []helpCommandEntry{
	{
		Command:   "up",
		Group:     "Local dev",
		Summary:   "Start a supervised local dev runtime.",
		Usage:     []string{"scenery up [--port <n>] [--listen <addr>] [--app-root <path>] [--claim-aliases] [-v|--verbose] [--json] [--detach]"},
		Flags:     []string{"--port <n>", "--listen <addr>", "--app-root <path>", "--claim-aliases", "-v, --verbose", "--json", "--detach"},
		JSON:      true,
		Stability: "stable",
	},
	{
		Command:   "ps",
		Group:     "Local dev",
		Summary:   "Show local dev app roots.",
		Usage:     []string{"scenery ps [--json] [--app-root <path>] [--watch]"},
		Flags:     []string{"--json", "--app-root <path>", "--watch"},
		Notes:     []string{"Human table output is the default.", "`--json` emits scenery.agent.status.v1 for agents and automation."},
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
			"--limit <n>", "--fields <csv>", "--json", "--jsonl",
			"--stream all|stdout|stderr", "--source <id>", "--kind <kind>",
			"--level <level>", "--grep <text>", "-f, --follow",
		},
		JSON:      true,
		Stability: "stable",
	},
	{
		Command:   "console",
		Group:     "Local dev",
		Summary:   "Open the source-aware dev console.",
		Usage:     []string{"scenery console [--app-root <path>] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>] [--backend auto|victoria]"},
		Flags:     []string{"--app-root <path>", "--source <id>", "--kind <kind>", "--level <level>", "--grep <text>", "--since <duration>", "--backend auto|victoria"},
		Stability: "stable",
	},
	{
		Command:   "down",
		Group:     "Local dev",
		Summary:   "Stop a local dev runtime.",
		Usage:     []string{"scenery down [--app-root <path>] [--db] [--state] [--all] [--json]"},
		Flags:     []string{"--app-root <path>", "--db", "--state", "--all", "--json"},
		JSON:      true,
		Stability: "stable",
	},
	{
		Command:   "prune",
		Group:     "Local dev",
		Summary:   "Remove old stopped session state.",
		Usage:     []string{"scenery prune --older-than <duration> [--app-root <path>] [--json]"},
		Flags:     []string{"--older-than <duration>", "--app-root <path>", "--json"},
		JSON:      true,
		Stability: "stable",
	},
	{
		Command: "db",
		Group:   "Database",
		Summary: "Manage database lifecycle, snapshots, branches, and local Postgres.",
		Usage: []string{
			"scenery db psql [--app-root <path>] [psql args...]",
			"scenery db apply|seed|setup|reset|drop [--app-root <path>] [--json]",
			"scenery db snapshot create|restore <name> [--app-root <path>]",
			"scenery db branch <command> [--app-root <path>] [--json]",
			"scenery db postgres <command> [--json]",
		},
		Subcommands: []string{"psql", "apply", "seed", "setup", "reset", "drop", "snapshot", "branch", "postgres"},
		Flags:       []string{"--app-root <path>", "--json", "--dry-run"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command:     "db branch",
		Group:       "Database",
		Summary:     "Manage local database branch leases.",
		Usage:       []string{"scenery db branch status|list|checkout|reset|delete|restore|diff|expire|prune [--app-root <path>] [--json]"},
		Subcommands: []string{"status", "list", "checkout", "reset", "delete", "restore", "diff", "expire", "prune"},
		Flags:       []string{"--app-root <path>", "--json"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command:     "db postgres",
		Group:       "Database",
		Summary:     "Manage the local Postgres dev cell.",
		Usage:       []string{"scenery db postgres install|start|status|logs|stop|restart|uninstall [--json]"},
		Subcommands: []string{"install", "start", "status", "logs", "stop", "restart", "uninstall"},
		Flags:       []string{"--app-root <path>", "--json"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command:     "worktree",
		Group:       "Workspace",
		Summary:     "Create, list, and remove app worktrees.",
		Usage:       []string{"scenery worktree create <name> [--from <branch>] [--app-root <path>] [--json]", "scenery worktree list [--app-root <path>] [--json]", "scenery worktree remove <name> [--app-root <path>] [--db] [--json]"},
		Subcommands: []string{"create", "list", "remove"},
		Flags:       []string{"--from <branch>", "--app-root <path>", "--db", "--json"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command:     "generate",
		Group:       "Generation",
		Summary:     "Generate configured outputs, clients, and SQLC artifacts.",
		Usage:       []string{"scenery generate [--app-root <path>] [--dry-run] [--json]", "scenery generate client [<app-id>] [--lang typescript] [--output <path>] [--app-root <path>] [--dry-run] [--json]", "scenery generate sqlc [--app-root <path>] [--dry-run] [--json]"},
		Subcommands: []string{"client", "sqlc"},
		Flags:       []string{"--app-root <path>", "--dry-run", "--json", "--lang typescript", "--output <path>"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command:     "task",
		Group:       "Tasks",
		Summary:     "List, inspect, graph, and run app tasks.",
		Usage:       []string{"scenery task list [--app-root <path>] [--json]", "scenery task inspect <target> [--app-root <path>] [--lang go|typescript] [--json]", "scenery task run <target> [--app-root <path>] [--env <name>] [--lang go|typescript] [-- script args...]", "scenery task graph --json [--app-root <path>]"},
		Subcommands: []string{"list", "inspect", "run", "graph"},
		Flags:       []string{"--app-root <path>", "--env <name>", "--lang go|typescript", "--json"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command:     "validate",
		Group:       "Validation",
		Summary:     "Run app-owned validation profiles.",
		Usage:       []string{"scenery validate [<profile>] [--app-root <path>] [--json] [--write] [--dry-run]", "scenery validate list [--app-root <path>] [--json]", "scenery validate inspect <profile> [--app-root <path>] [--json]", "scenery validate graph [<profile>] [--app-root <path>] --json", "scenery validate changed [--base <ref>] [--app-root <path>] [--json] [--write] [--dry-run]"},
		Subcommands: []string{"list", "inspect", "graph", "changed"},
		Flags:       []string{"--app-root <path>", "--base <ref>", "--json", "--write", "--dry-run"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command:   "serve",
		Group:     "Runtime",
		Summary:   "Run the API server once.",
		Usage:     []string{"scenery serve [--port <n>] [--listen <addr>] [--app-root <path>] [--env <name>] [--log-format text|json]"},
		Flags:     []string{"--port <n>", "--listen <addr>", "--app-root <path>", "--env <name>", "--log-format text|json"},
		Stability: "stable",
	},
	{
		Command: "worker",
		Group:   "Runtime",
		Summary: "Run workers and manage worker deployments.",
		Usage: []string{
			"scenery worker [--task-queue <name>[,<name>]]... [--app-root <path>] [--env <name>] [--log-format text|json]",
			"scenery worker bindings [--app-root <path>] [--out <dir>] [--json]",
			"scenery worker typescript [--task-queue <name>[,<name>]]... [--runtime bun|node] [--app-root <path>] [--generate-only]",
			"scenery worker deployment set-current|ramp|drain [flags]",
		},
		Subcommands: []string{"bindings", "typescript", "deployment"},
		Flags:       []string{"--task-queue <name>[,<name>]", "--app-root <path>", "--env <name>", "--log-format text|json", "--json"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command:     "worker deployment",
		Group:       "Runtime",
		Summary:     "Promote, ramp, and drain worker deployments.",
		Usage:       []string{"scenery worker deployment set-current --build-id <id> [--deployment <name>] [--ignore-missing-task-queues] [--allow-no-pollers] [--app-root <path>] [--json]", "scenery worker deployment ramp --build-id <id> --percentage <0-100> [--deployment <name>] [--ignore-missing-task-queues] [--allow-no-pollers] [--app-root <path>] [--json]", "scenery worker deployment drain --build-id <id> [--deployment <name>] [--force] [--app-root <path>] [--json]"},
		Subcommands: []string{"set-current", "ramp", "drain"},
		Flags:       []string{"--build-id <id>", "--percentage <0-100>", "--deployment <name>", "--ignore-missing-task-queues", "--allow-no-pollers", "--force", "--app-root <path>", "--json"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command:   "build",
		Group:     "Build and checks",
		Summary:   "Build the deployable binary.",
		Usage:     []string{"scenery build [--app-root <path>] [-o <path>]"},
		Flags:     []string{"--app-root <path>", "-o <path>"},
		Stability: "stable",
	},
	{
		Command:   "check",
		Group:     "Build and checks",
		Summary:   "Check the app model.",
		Usage:     []string{"scenery check [--app-root <path>] [--json]"},
		Flags:     []string{"--app-root <path>", "--json"},
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
		Usage:       []string{"scenery harness [--app-root <path>] [--json] [--write] [--with-validation[=<profile>]]", "scenery harness self [--repo-root <path>] [--summary|--json|--json=summary|--json=full] [--write] [--quick|--race|--release] [--fresh-tests]", "scenery harness ui --json [--app-root <path>] [--dashboard-url <url>] [--headed] [--write]"},
		Subcommands: []string{"self", "ui"},
		Flags:       []string{"--app-root <path>", "--repo-root <path>", "--summary", "--json", "--json=summary", "--json=full", "--write", "--quick", "--race", "--release", "--fresh-tests", "--dashboard-url <url>", "--headed", "--with-validation[=<profile>]"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command: "inspect",
		Group:   "Inspection",
		Summary: "Inspect app model and diagnostics as JSON.",
		Usage: []string{
			"scenery inspect app|routes|services|endpoints|wire|build|paths|generators|temporal|observability|validation --json [--app-root <path>]",
			"scenery inspect docs --json [--repo-root <path>]",
			"scenery inspect harness [artifact <name>|diagnostics --severity error|warning|timing --top <n>] --json [--app-root <path>] [--repo-root <path>]",
		},
		Subcommands: []string{"app", "routes", "services", "endpoints", "wire", "build", "paths", "generators", "temporal", "observability", "validation", "docs", "harness"},
		Flags:       []string{"--json", "--app-root <path>", "--repo-root <path>"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command:     "inspect harness",
		Group:       "Inspection",
		Summary:     "Inspect harness artifacts, diagnostics, and timings.",
		Usage:       []string{"scenery inspect harness [artifact <name>|diagnostics --severity error|warning|timing --top <n>] --json [--app-root <path>] [--repo-root <path>]"},
		Subcommands: []string{"artifact", "diagnostics", "timing"},
		Flags:       []string{"--json", "--app-root <path>", "--repo-root <path>", "--severity error|warning", "--top <n>"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command:     "traces",
		Group:       "Observability",
		Summary:     "List or clear local traces.",
		Usage:       []string{"scenery traces list [--json] [--app-root <path>] [--service <name>] [--endpoint <name>] [--trace-id <id>] [--status ok|error] [--min-duration-ms <n>] [--since <duration>] [--limit <n>] [--slowest]", "scenery traces clear --json [--app-root <path>]"},
		Subcommands: []string{"list", "clear"},
		Flags:       []string{"--json", "--app-root <path>", "--service <name>", "--endpoint <name>", "--trace-id <id>", "--status ok|error", "--min-duration-ms <n>", "--since <duration>", "--limit <n>", "--slowest"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command:     "metrics",
		Group:       "Observability",
		Summary:     "List, query, and inspect local metrics.",
		Usage:       []string{"scenery metrics list [--json] [--app-root <path>] [--service <name>] [--endpoint <name>] [--status ok|error] [--since <duration>] [--limit <n>]", "scenery metrics query [--json] [--app-root <path>] --promql <query> [--instant] [--since <duration>] [--start <time>] [--end <time>] [--step <duration>] [--timeout <duration>] [--limit <n>]", "scenery metrics labels|series [--json] [--app-root <path>] --match <selector> [flags]"},
		Subcommands: []string{"list", "query", "labels", "series"},
		Flags:       []string{"--json", "--app-root <path>", "--promql <query>", "--match <selector>", "--instant", "--since <duration>", "--start <time>", "--end <time>", "--step <duration>", "--timeout <duration>", "--limit <n>"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command:   "doctor",
		Group:     "System",
		Summary:   "Check host and app readiness.",
		Usage:     []string{"scenery doctor [--app-root <path>] [--json]"},
		Flags:     []string{"--app-root <path>", "--json"},
		JSON:      true,
		Stability: "stable",
	},
	{
		Command:   "version",
		Group:     "System",
		Summary:   "Print version information.",
		Usage:     []string{"scenery version [--json]"},
		Flags:     []string{"--json"},
		JSON:      true,
		Stability: "stable",
	},
	{
		Command:     "system",
		Group:       "System",
		Summary:     "Manage agent, edge, trust, and toolchain.",
		Usage:       []string{"scenery system agent [--socket <path>] [--router-listen <addr>] [--router-tls|--router-http] [--trust] [--json]", "scenery system agent restart [--socket <path>] [--router-listen <addr>] [--router-tls|--router-http] [--trust] [--json]", "scenery system edge install|trust|status|restart|uninstall|dns|privileged [--json]", "scenery system toolchain list|sync|verify [--json] [--tool <name>] [--images]", "scenery system toolchain path [--json] --tool <name>", "scenery system trust [--json]"},
		Subcommands: []string{"agent", "edge", "toolchain", "trust"},
		Flags:       []string{"--socket <path>", "--router-listen <addr>", "--router-tls", "--router-http", "--trust", "--json", "--tool <name>", "--images"},
		JSON:        true,
		Stability:   "stable",
	},
}

func helpCommand(args []string) error {
	if len(args) == 0 {
		writeRootHelp(os.Stdout)
		return nil
	}
	if len(args) == 1 && args[0] == "--json" {
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
		return fmt.Errorf("unknown help topic %q", strings.Join(args, " "))
	}
	writeCommandHelp(os.Stdout, entry)
	return nil
}

func rootHelpString() string {
	var b strings.Builder
	writeRootHelp(&b)
	return strings.TrimRight(b.String(), "\n")
}

func writeRootHelp(w io.Writer) {
	fmt.Fprintln(w, "Scenery - build, run, and inspect app services.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  scenery <command> [args] [flags]")
	fmt.Fprintln(w, "  scenery help <command>")
	fmt.Fprintln(w, "  scenery help all")
	fmt.Fprintln(w, "  scenery help --json")
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
	fmt.Fprintln(w, `Use "scenery help --json" for the machine-readable command manifest.`)
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
		SchemaVersion: helpManifestSchemaVersion,
		Commands:      append([]helpCommandEntry(nil), helpCommands...),
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(manifest)
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
