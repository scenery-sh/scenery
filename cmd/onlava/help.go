package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

const helpManifestSchemaVersion = "onlava.help.v1"

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
	{Name: "Local session", Entries: []helpRootEntry{
		{Command: "up", Summary: "Start a local dev session"},
		{Command: "ps", Summary: "Show local sessions"},
		{Command: "logs", Summary: "Read, follow, or query session logs"},
		{Command: "console", Summary: "Open the source-aware dev console"},
		{Command: "down", Summary: "Stop a session"},
		{Command: "prune", Summary: "Remove old stopped session state"},
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
		{Command: "db", Summary: "Manage database lifecycle, branches, and local Neon"},
		{Command: "task", Summary: "List, inspect, graph, and run app tasks"},
		{Command: "validate", Summary: "Run validation profiles"},
		{Command: "harness", Summary: "Run Onlava harnesses"},
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
	{Name: "Local session", Commands: []string{
		"onlava up",
		"onlava ps",
		"onlava logs",
		"onlava logs query",
		"onlava logs tail",
		"onlava console",
		"onlava down",
		"onlava prune",
	}},
	{Name: "Database", Commands: []string{
		"onlava db psql",
		"onlava db apply",
		"onlava db seed",
		"onlava db setup",
		"onlava db reset",
		"onlava db drop",
		"onlava db snapshot create",
		"onlava db snapshot restore",
		"onlava db branch status",
		"onlava db branch list",
		"onlava db branch checkout",
		"onlava db branch reset",
		"onlava db branch delete",
		"onlava db branch restore",
		"onlava db branch diff",
		"onlava db branch expire",
		"onlava db branch prune",
		"onlava db neon install",
		"onlava db neon start",
		"onlava db neon status",
		"onlava db neon logs",
		"onlava db neon stop",
		"onlava db neon restart",
		"onlava db neon uninstall",
	}},
	{Name: "Workspace", Commands: []string{
		"onlava worktree create",
		"onlava worktree list",
		"onlava worktree remove",
	}},
	{Name: "Generation", Commands: []string{
		"onlava generate",
		"onlava generate client",
		"onlava generate sqlc",
	}},
	{Name: "Tasks", Commands: []string{
		"onlava task list",
		"onlava task inspect",
		"onlava task run",
		"onlava task graph",
	}},
	{Name: "Validation", Commands: []string{
		"onlava validate",
		"onlava validate list",
		"onlava validate inspect",
		"onlava validate graph",
		"onlava validate changed",
	}},
	{Name: "Runtime", Commands: []string{
		"onlava serve",
		"onlava worker",
		"onlava worker bindings",
		"onlava worker typescript",
		"onlava worker deployment set-current",
		"onlava worker deployment ramp",
		"onlava worker deployment drain",
	}},
	{Name: "Build and checks", Commands: []string{
		"onlava build",
		"onlava check",
		"onlava test",
	}},
	{Name: "Harness", Commands: []string{
		"onlava harness",
		"onlava harness self",
		"onlava harness ui",
	}},
	{Name: "Inspection", Commands: []string{
		"onlava inspect app",
		"onlava inspect routes",
		"onlava inspect services",
		"onlava inspect endpoints",
		"onlava inspect wire",
		"onlava inspect build",
		"onlava inspect paths",
		"onlava inspect generators",
		"onlava inspect temporal",
		"onlava inspect observability",
		"onlava inspect validation",
		"onlava inspect docs",
		"onlava inspect harness artifact",
		"onlava inspect harness diagnostics",
		"onlava inspect harness timing",
	}},
	{Name: "Observability", Commands: []string{
		"onlava traces list",
		"onlava traces clear",
		"onlava metrics list",
		"onlava metrics query",
		"onlava metrics labels",
		"onlava metrics series",
	}},
	{Name: "System", Commands: []string{
		"onlava doctor",
		"onlava version",
		"onlava system agent",
		"onlava system agent restart",
		"onlava system edge",
		"onlava system toolchain",
		"onlava system trust",
	}},
}

var helpCommands = []helpCommandEntry{
	{
		Command:   "up",
		Group:     "Local session",
		Summary:   "Start a supervised local dev session.",
		Usage:     []string{"onlava up [--port <n>] [--listen <addr>] [--app-root <path>] [--session <id>|--new-session] [--claim-aliases] [-v|--verbose] [--json] [--detach]"},
		Flags:     []string{"--port <n>", "--listen <addr>", "--app-root <path>", "--session <id>", "--new-session", "--claim-aliases", "-v, --verbose", "--json", "--detach"},
		JSON:      true,
		Stability: "stable",
	},
	{
		Command:   "ps",
		Group:     "Local session",
		Summary:   "Show local dev sessions.",
		Usage:     []string{"onlava ps [--json] [--app-root <path>] [--session <id>] [--watch]"},
		Flags:     []string{"--json", "--app-root <path>", "--session <id>", "--watch"},
		Notes:     []string{"Human table output is the default.", "`--json` emits onlava.agent.status.v1 for agents and automation."},
		JSON:      true,
		Stability: "stable",
	},
	{
		Command:     "logs",
		Group:       "Local session",
		Summary:     "Read, follow, or query session logs.",
		Usage:       []string{"onlava logs [flags]", "onlava logs query --query <logsql> [flags]", "onlava logs tail --query <logsql> [flags]"},
		Subcommands: []string{"query", "tail"},
		Flags: []string{
			"--app-root <path>", "--session current|<id>", "--since <duration>",
			"--limit <n>", "--fields <csv>", "--json", "--jsonl",
			"--stream all|stdout|stderr", "--source <id>", "--kind <kind>",
			"--level <level>", "--grep <text>", "-f, --follow",
		},
		JSON:      true,
		Stability: "stable",
	},
	{
		Command:   "console",
		Group:     "Local session",
		Summary:   "Open the source-aware dev console.",
		Usage:     []string{"onlava console [--app-root <path>] [--session current|<id>] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>] [--backend auto|victoria]"},
		Flags:     []string{"--app-root <path>", "--session current|<id>", "--source <id>", "--kind <kind>", "--level <level>", "--grep <text>", "--since <duration>", "--backend auto|victoria"},
		Stability: "stable",
	},
	{
		Command:   "down",
		Group:     "Local session",
		Summary:   "Stop a local session.",
		Usage:     []string{"onlava down [--app-root <path>] [--session <id>] [--db] [--state] [--all] [--json]"},
		Flags:     []string{"--app-root <path>", "--session <id>", "--db", "--state", "--all", "--json"},
		JSON:      true,
		Stability: "stable",
	},
	{
		Command:   "prune",
		Group:     "Local session",
		Summary:   "Remove old stopped session state.",
		Usage:     []string{"onlava prune --older-than <duration> [--app-root <path>] [--json]"},
		Flags:     []string{"--older-than <duration>", "--app-root <path>", "--json"},
		JSON:      true,
		Stability: "stable",
	},
	{
		Command: "db",
		Group:   "Database",
		Summary: "Manage database lifecycle, snapshots, branches, and local Neon.",
		Usage: []string{
			"onlava db psql [--app-root <path>] [psql args...]",
			"onlava db apply|seed|setup|reset|drop [--app-root <path>] [--json]",
			"onlava db snapshot create|restore <name> [--app-root <path>]",
			"onlava db branch <command> [--app-root <path>] [--json]",
			"onlava db neon <command> [--json]",
		},
		Subcommands: []string{"psql", "apply", "seed", "setup", "reset", "drop", "snapshot", "branch", "neon"},
		Flags:       []string{"--app-root <path>", "--json", "--dry-run"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command:     "db branch",
		Group:       "Database",
		Summary:     "Manage local Neon database branch leases.",
		Usage:       []string{"onlava db branch status|list|checkout|reset|delete|restore|diff|expire|prune [--app-root <path>] [--json]"},
		Subcommands: []string{"status", "list", "checkout", "reset", "delete", "restore", "diff", "expire", "prune"},
		Flags:       []string{"--app-root <path>", "--json"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command:     "db neon",
		Group:       "Database",
		Summary:     "Manage the local Neon dev cell.",
		Usage:       []string{"onlava db neon install|start|status|logs|stop|restart|uninstall [--json]"},
		Subcommands: []string{"install", "start", "status", "logs", "stop", "restart", "uninstall"},
		Flags:       []string{"--json"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command:     "worktree",
		Group:       "Workspace",
		Summary:     "Create, list, and remove app worktrees.",
		Usage:       []string{"onlava worktree create <name> [--from <branch>] [--app-root <path>] [--json]", "onlava worktree list [--app-root <path>] [--json]", "onlava worktree remove <name> [--app-root <path>] [--db] [--json]"},
		Subcommands: []string{"create", "list", "remove"},
		Flags:       []string{"--from <branch>", "--app-root <path>", "--db", "--json"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command:     "generate",
		Group:       "Generation",
		Summary:     "Generate configured outputs, clients, and SQLC artifacts.",
		Usage:       []string{"onlava generate [--app-root <path>] [--dry-run] [--json]", "onlava generate client [<app-id>] [--lang typescript] [--output <path>] [--app-root <path>] [--dry-run] [--json]", "onlava generate sqlc [--app-root <path>] [--dry-run] [--json]"},
		Subcommands: []string{"client", "sqlc"},
		Flags:       []string{"--app-root <path>", "--dry-run", "--json", "--lang typescript", "--output <path>"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command:     "task",
		Group:       "Tasks",
		Summary:     "List, inspect, graph, and run app tasks.",
		Usage:       []string{"onlava task list [--app-root <path>] [--json]", "onlava task inspect <target> [--app-root <path>] [--lang go|typescript] [--json]", "onlava task run <target> [--app-root <path>] [--env <name>] [--lang go|typescript] [-- script args...]", "onlava task graph --json [--app-root <path>]"},
		Subcommands: []string{"list", "inspect", "run", "graph"},
		Flags:       []string{"--app-root <path>", "--env <name>", "--lang go|typescript", "--json"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command:     "validate",
		Group:       "Validation",
		Summary:     "Run app-owned validation profiles.",
		Usage:       []string{"onlava validate [<profile>] [--app-root <path>] [--json] [--write] [--dry-run]", "onlava validate list [--app-root <path>] [--json]", "onlava validate inspect <profile> [--app-root <path>] [--json]", "onlava validate graph [<profile>] [--app-root <path>] --json", "onlava validate changed [--base <ref>] [--app-root <path>] [--json] [--write] [--dry-run]"},
		Subcommands: []string{"list", "inspect", "graph", "changed"},
		Flags:       []string{"--app-root <path>", "--base <ref>", "--json", "--write", "--dry-run"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command:   "serve",
		Group:     "Runtime",
		Summary:   "Run the API server once.",
		Usage:     []string{"onlava serve [--port <n>] [--listen <addr>] [--app-root <path>] [--env <name>] [--log-format text|json]"},
		Flags:     []string{"--port <n>", "--listen <addr>", "--app-root <path>", "--env <name>", "--log-format text|json"},
		Stability: "stable",
	},
	{
		Command: "worker",
		Group:   "Runtime",
		Summary: "Run workers and manage worker deployments.",
		Usage: []string{
			"onlava worker [--task-queue <name>[,<name>]]... [--app-root <path>] [--env <name>] [--log-format text|json]",
			"onlava worker bindings [--app-root <path>] [--out <dir>] [--json]",
			"onlava worker typescript [--task-queue <name>[,<name>]]... [--runtime bun|node] [--app-root <path>] [--generate-only]",
			"onlava worker deployment set-current|ramp|drain [flags]",
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
		Usage:       []string{"onlava worker deployment set-current --build-id <id> [--deployment <name>] [--ignore-missing-task-queues] [--allow-no-pollers] [--app-root <path>] [--json]", "onlava worker deployment ramp --build-id <id> --percentage <0-100> [--deployment <name>] [--ignore-missing-task-queues] [--allow-no-pollers] [--app-root <path>] [--json]", "onlava worker deployment drain --build-id <id> [--deployment <name>] [--force] [--app-root <path>] [--json]"},
		Subcommands: []string{"set-current", "ramp", "drain"},
		Flags:       []string{"--build-id <id>", "--percentage <0-100>", "--deployment <name>", "--ignore-missing-task-queues", "--allow-no-pollers", "--force", "--app-root <path>", "--json"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command:   "build",
		Group:     "Build and checks",
		Summary:   "Build the deployable binary.",
		Usage:     []string{"onlava build [--app-root <path>] [-o <path>]"},
		Flags:     []string{"--app-root <path>", "-o <path>"},
		Stability: "stable",
	},
	{
		Command:   "check",
		Group:     "Build and checks",
		Summary:   "Check the app model.",
		Usage:     []string{"onlava check [--app-root <path>] [--json]"},
		Flags:     []string{"--app-root <path>", "--json"},
		JSON:      true,
		Stability: "stable",
	},
	{
		Command:   "test",
		Group:     "Build and checks",
		Summary:   "Run Go tests for an app.",
		Usage:     []string{"onlava test [--app-root <path>] [go test flags/packages...]"},
		Flags:     []string{"--app-root <path>"},
		Stability: "stable",
	},
	{
		Command:     "harness",
		Group:       "Harness",
		Summary:     "Run framework and UI harnesses.",
		Usage:       []string{"onlava harness [--app-root <path>] [--json] [--write] [--with-validation[=<profile>]]", "onlava harness self [--repo-root <path>] [--summary|--json|--json=summary|--json=full] [--write] [--quick|--race|--release] [--with-neon-selfhost]", "onlava harness ui --json [--app-root <path>] [--dashboard-url <url>] [--headed] [--write]"},
		Subcommands: []string{"self", "ui"},
		Flags:       []string{"--app-root <path>", "--repo-root <path>", "--summary", "--json", "--json=summary", "--json=full", "--write", "--quick", "--race", "--release", "--with-neon-selfhost", "--dashboard-url <url>", "--headed", "--with-validation[=<profile>]"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command: "inspect",
		Group:   "Inspection",
		Summary: "Inspect app model and diagnostics as JSON.",
		Usage: []string{
			"onlava inspect app|routes|services|endpoints|wire|build|paths|generators|temporal|observability|validation --json [--app-root <path>]",
			"onlava inspect docs --json [--repo-root <path>]",
			"onlava inspect harness [artifact <name>|diagnostics --severity error|warning|timing --top <n>] --json [--app-root <path>] [--repo-root <path>]",
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
		Usage:       []string{"onlava inspect harness [artifact <name>|diagnostics --severity error|warning|timing --top <n>] --json [--app-root <path>] [--repo-root <path>]"},
		Subcommands: []string{"artifact", "diagnostics", "timing"},
		Flags:       []string{"--json", "--app-root <path>", "--repo-root <path>", "--severity error|warning", "--top <n>"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command:     "traces",
		Group:       "Observability",
		Summary:     "List or clear local traces.",
		Usage:       []string{"onlava traces list [--json] [--session current|<id>] [--service <name>] [--endpoint <name>] [--trace-id <id>] [--status ok|error] [--min-duration-ms <n>] [--since <duration>] [--limit <n>] [--slowest] [--app-root <path>]", "onlava traces clear --json [--app-root <path>]"},
		Subcommands: []string{"list", "clear"},
		Flags:       []string{"--json", "--session current|<id>", "--service <name>", "--endpoint <name>", "--trace-id <id>", "--status ok|error", "--min-duration-ms <n>", "--since <duration>", "--limit <n>", "--slowest", "--app-root <path>"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command:     "metrics",
		Group:       "Observability",
		Summary:     "List, query, and inspect local metrics.",
		Usage:       []string{"onlava metrics list [--json] [--session current|<id>] [--service <name>] [--endpoint <name>] [--status ok|error] [--since <duration>] [--limit <n>] [--app-root <path>]", "onlava metrics query [--json] [--app-root <path>] [--session current|<id>] --promql <query> [--instant] [--since <duration>] [--start <time>] [--end <time>] [--step <duration>] [--timeout <duration>] [--limit <n>]", "onlava metrics labels|series [--json] [--app-root <path>] [--session current|<id>] --match <selector> [flags]"},
		Subcommands: []string{"list", "query", "labels", "series"},
		Flags:       []string{"--json", "--app-root <path>", "--session current|<id>", "--promql <query>", "--match <selector>", "--instant", "--since <duration>", "--start <time>", "--end <time>", "--step <duration>", "--timeout <duration>", "--limit <n>"},
		JSON:        true,
		Stability:   "stable",
	},
	{
		Command:   "doctor",
		Group:     "System",
		Summary:   "Check host and app readiness.",
		Usage:     []string{"onlava doctor [--app-root <path>] [--json]"},
		Flags:     []string{"--app-root <path>", "--json"},
		JSON:      true,
		Stability: "stable",
	},
	{
		Command:   "version",
		Group:     "System",
		Summary:   "Print version information.",
		Usage:     []string{"onlava version [--json]"},
		Flags:     []string{"--json"},
		JSON:      true,
		Stability: "stable",
	},
	{
		Command:     "system",
		Group:       "System",
		Summary:     "Manage agent, edge, trust, and toolchain.",
		Usage:       []string{"onlava system agent [--socket <path>] [--router-listen <addr>] [--router-tls|--router-http] [--trust] [--json]", "onlava system agent restart [--socket <path>] [--router-listen <addr>] [--router-tls|--router-http] [--trust] [--json]", "onlava system edge install|trust|status|restart|uninstall|dns|privileged [--json]", "onlava system toolchain list|sync|verify|path [--json]", "onlava system trust [--json]"},
		Subcommands: []string{"agent", "edge", "toolchain", "trust"},
		Flags:       []string{"--socket <path>", "--router-listen <addr>", "--router-tls", "--router-http", "--trust", "--json"},
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

func usageError() error {
	return fmt.Errorf("%s", rootHelpString())
}

func rootHelpString() string {
	var b strings.Builder
	writeRootHelp(&b)
	return strings.TrimRight(b.String(), "\n")
}

func writeRootHelp(w io.Writer) {
	fmt.Fprintln(w, "Onlava - build, run, and inspect app services.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  onlava <command> [args] [flags]")
	fmt.Fprintln(w, "  onlava help <command>")
	fmt.Fprintln(w, "  onlava help all")
	fmt.Fprintln(w, "  onlava help --json")
	for _, group := range rootHelpGroups {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "%s:\n", group.Name)
		for _, entry := range group.Entries {
			fmt.Fprintf(w, "  %-10s %s\n", entry.Command, entry.Summary)
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, `Use "onlava help <command>" for flags and subcommands.`)
}

func writeHelpAll(w io.Writer) {
	fmt.Fprintln(w, "Onlava command reference")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  onlava <command> [args] [flags]")
	fmt.Fprintln(w, "  onlava help <command>")
	for _, group := range helpReferenceGroups {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "%s:\n", group.Name)
		for _, command := range group.Commands {
			fmt.Fprintf(w, "  %s\n", command)
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, `Use "onlava help <command>" for exact flags.`)
	fmt.Fprintln(w, `Use "onlava help --json" for the machine-readable command manifest.`)
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
