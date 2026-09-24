package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"scenery.sh/internal/envpolicy"
)

const harnessCLIGrammarProbeName = "CLI grammar probe"

// The probe reads the grammar the product advertises in `scenery help -o json`
// and holds the parser to it in both directions: a request written wrongly is an
// invalid request that names the mistake and never an internal failure, and a
// request written as advertised is never refused as wrongly written.
//
// Families that act on the host instead of the disposable app are read from the
// grammar but not executed.
var harnessCLIGrammarHostFamilies = []string{"system", "deploy"}

// harnessCLIGrammarReadOnly lists the usage lines that are safe to execute as
// written: they read the disposable app or the disposable home.
var harnessCLIGrammarReadOnly = []string{
	"scenery compile", "scenery schema", "scenery list", "scenery get", "scenery explain", "scenery ps", "scenery logs query",
	"scenery db list", "scenery db server status", "scenery framework inspect", "scenery task list", "scenery task inspect",
	"scenery task graph", "scenery storage ls", "scenery storage stat", "scenery snapshot verify", "scenery validate list",
	"scenery validate inspect", "scenery validate graph", "scenery check", "scenery inspect", "scenery assistant status",
	"scenery traces list", "scenery metrics list", "scenery metrics query", "scenery metrics labels", "scenery metrics series",
	"scenery telemetry", "scenery doctor", "scenery version",
}

var harnessCLIGrammarRefusal = regexp.MustCompile(`^(unknown (flag|command|argument|[a-z -]+ (command|subcommand|subject|topic))|unexpected argument|unsupported output|missing value for|expected .* before)`)

type harnessCLIGrammarCommand struct {
	Command     string   `json:"command"`
	Usage       []string `json:"usage"`
	Subcommands []string `json:"subcommands"`
}

type harnessCLIGrammarCase struct {
	kind string // "refuse", "accept" or "help"
	args []string
}

type harnessCLIGrammarOutcome struct {
	exit    int
	code    string
	message string
	json    bool
	kind    string
	stdout  string
}

func runHarnessCLIGrammarProbeStep(ctx context.Context, repoRoot string) harnessStep {
	started := time.Now()
	step := harnessStep{Name: harnessCLIGrammarProbeName, Command: []string{"go", "run", "./scripts/verify", "--probe", "cli-grammar", "--summary"}}
	var err error
	step.Summary, step.Diagnostics, err = runHarnessCLIGrammarProbeCheck(ctx, repoRoot)
	step.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		step.Error = strings.TrimSpace(err.Error())
		step.Diagnostics = append(step.Diagnostics, checkDiagnostic{Stage: step.Name, Severity: "error", Message: step.Error,
			SuggestedAction: "Fix the CLI grammar probe setup, then rerun `go run ./scripts/verify --probe cli-grammar --summary`."})
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

func runHarnessCLIGrammarProbeCheck(ctx context.Context, repoRoot string) (map[string]any, []checkDiagnostic, error) {
	binary := harnessLocalSceneryBinaryPath(repoRoot)
	// A short root keeps the agent socket inside the platform's path limit.
	home, err := os.MkdirTemp("/tmp", "scg.")
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = os.RemoveAll(home) }()
	app := filepath.Join(home, "app")
	if err := copyHarnessCLIGrammarFixture(filepath.Join(repoRoot, "internal", "compiler", "testdata", "native"), app); err != nil {
		return nil, nil, err
	}
	overrides := []string{"HOME=" + home, "SCENERY_AGENT_HOME=" + filepath.Join(home, ".scenery")}
	for _, name := range []string{"GOPATH", "GOCACHE", "GOMODCACHE"} {
		value, err := exec.CommandContext(ctx, "go", "env", name).Output()
		if err != nil {
			return nil, nil, fmt.Errorf("go env %s: %w", name, err)
		}
		overrides = append(overrides, name+"="+strings.TrimSpace(string(value)))
	}
	environment := envWithOverrides(envpolicy.Environ(), overrides...)
	run := func(args []string) (harnessCLIGrammarOutcome, error) {
		caseCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		command := exec.CommandContext(caseCtx, binary, args...)
		command.Dir, command.Env = app, environment
		var stdout, stderr strings.Builder
		command.Stdout, command.Stderr = &stdout, &stderr
		runErr := command.Run()
		outcome := harnessCLIGrammarOutcome{message: strings.TrimSpace(stderr.String()), stdout: stdout.String()}
		if exitErr, ok := errors.AsType[*exec.ExitError](runErr); ok {
			outcome.exit = exitErr.ExitCode()
		} else if runErr != nil {
			return outcome, runErr
		}
		if caseCtx.Err() != nil {
			return outcome, fmt.Errorf("timed out")
		}
		var envelope struct {
			Diagnostics []struct{ Code, Message string } `json:"diagnostics"`
			Data        struct {
				Kind       string                         `json:"kind"`
				Diagnostic struct{ Code, Message string } `json:"diagnostic"`
			} `json:"data"`
		}
		lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
		if json.Unmarshal([]byte(lines[len(lines)-1]), &envelope) == nil {
			outcome.json, outcome.kind = true, envelope.Data.Kind
			if len(envelope.Diagnostics) > 0 {
				outcome.code, outcome.message = envelope.Diagnostics[0].Code, envelope.Diagnostics[0].Message
			} else if envelope.Data.Diagnostic.Code != "" {
				outcome.code, outcome.message = envelope.Data.Diagnostic.Code, envelope.Data.Diagnostic.Message
			}
		}
		return outcome, nil
	}

	help, err := exec.CommandContext(ctx, binary, "help", "-o", "json").Output()
	if err != nil {
		return nil, nil, fmt.Errorf("scenery help -o json: %w", err)
	}
	var grammar struct {
		Data struct {
			Commands []harnessCLIGrammarCommand `json:"commands"`
		} `json:"data"`
	}
	if err := json.Unmarshal(help, &grammar); err != nil || len(grammar.Data.Commands) == 0 {
		return nil, nil, fmt.Errorf("scenery help -o json is not a grammar: %v", err)
	}
	cases, skipped := harnessCLIGrammarCases(grammar.Data.Commands)
	var diagnostics []checkDiagnostic
	report := func(testCase harnessCLIGrammarCase, outcome harnessCLIGrammarOutcome, problem string) {
		diagnostics = append(diagnostics, checkDiagnostic{Stage: harnessCLIGrammarProbeName, Severity: "error",
			Message:         fmt.Sprintf("scenery %s: %s (exit %d, %s %q)", strings.Join(testCase.args, " "), problem, outcome.exit, outcome.code, outcome.message),
			SuggestedAction: "Classify the refusal with usageErrorf, or make the parser accept the grammar `scenery help` advertises."})
	}
	counts := map[string]int{}
	for _, testCase := range cases {
		outcome, err := run(testCase.args)
		if err != nil {
			report(testCase, outcome, err.Error())
			continue
		}
		counts[testCase.kind]++
		switch {
		case testCase.kind == "refuse" && outcome.exit != 2:
			report(testCase, outcome, "a wrongly written request is not an invalid request")
		case testCase.kind == "refuse" && outcome.json && outcome.code != "SCN8001":
			report(testCase, outcome, "a wrongly written request does not carry SCN8001")
		case testCase.kind == "refuse" && outcome.message == "":
			report(testCase, outcome, "a wrongly written request names no mistake")
		case testCase.kind == "accept" && outcome.exit == 2 && harnessCLIGrammarRefusal.MatchString(outcome.message):
			report(testCase, outcome, "the advertised grammar is refused as wrongly written")
		case testCase.kind == "accept" && (outcome.exit == 10 || strings.HasPrefix(outcome.code, "SCN9")):
			report(testCase, outcome, "the advertised grammar fails internally")
		case testCase.kind == "help" && outcome.exit != 0:
			report(testCase, outcome, "a help request on an advertised command is refused")
		case testCase.kind == "help" && slices.Contains(testCase.args, "json") && outcome.kind != "scenery.help":
			report(testCase, outcome, "a JSON help request does not answer with the command's help descriptor")
		case testCase.kind == "help" && !slices.Contains(testCase.args, "json") && !strings.Contains(outcome.stdout, "Usage:"):
			report(testCase, outcome, "a help request does not print the command's usage")
		}
	}
	return map[string]any{"commands": len(grammar.Data.Commands), "refused": counts["refuse"], "accepted": counts["accept"], "help_requests": counts["help"], "not_executed": skipped}, diagnostics, nil
}

// harnessCLIGrammarCases derives the invocations from the advertised usage
// lines. Every line yields requests that must be refused and help requests
// (`-h`, `--help`, and `--help -o json`) that must answer with help, including
// on host families, since a help request never runs its command; a read-only
// line also yields itself, written with its required parts only, which must be
// accepted.
func harnessCLIGrammarCases(commands []harnessCLIGrammarCommand) ([]harnessCLIGrammarCase, []string) {
	var cases []harnessCLIGrammarCase
	var skipped []string
	seen := map[string]bool{}
	add := func(kind string, args ...string) {
		if key := kind + "\x00" + strings.Join(args, "\x00"); !seen[key] {
			seen[key] = true
			cases = append(cases, harnessCLIGrammarCase{kind: kind, args: args})
		}
	}
	add("refuse", "zz-unknown-command")
	for _, command := range commands {
		for _, usage := range command.Usage {
			for _, path := range harnessCLIGrammarParseUsage(usage).paths {
				add("help", append(slices.Clone(path), "-h")...)
				add("help", append(slices.Clone(path), "--help")...)
				add("help", append(slices.Clone(path), "--help", "-o", "json")...)
			}
		}
		family, _, _ := strings.Cut(command.Command, " ")
		if slices.Contains(harnessCLIGrammarHostFamilies, family) {
			skipped = append(skipped, command.Command)
			continue
		}
		words := strings.Fields(command.Command)
		if len(command.Subcommands) > 0 && !harnessCLIGrammarTakesOperand(command) {
			add("refuse", append(slices.Clone(words), "zz-unknown-subcommand")...)
		}
		for _, usage := range command.Usage {
			line := harnessCLIGrammarParseUsage(usage)
			if line.passthrough {
				continue
			}
			for _, path := range line.paths {
				add("refuse", append(slices.Clone(path), "--zz-unknown-flag")...)
				for _, flag := range line.valueFlags {
					add("refuse", append(slices.Clone(path), flag)...)
				}
				for flag, choices := range line.choiceFlags {
					if !slices.Contains(choices, "zz-invalid") {
						add("refuse", append(slices.Clone(path), flag, "zz-invalid")...)
					}
				}
				if !slices.ContainsFunc(harnessCLIGrammarReadOnly, func(prefix string) bool {
					return strings.HasPrefix(strings.Join(path, " ")+" ", strings.TrimPrefix(prefix, "scenery ")+" ")
				}) {
					continue
				}
				for _, output := range line.outputs {
					add("accept", append(append(slices.Clone(path), line.required...), "-o", output)...)
				}
			}
		}
	}
	return cases, skipped
}

func harnessCLIGrammarTakesOperand(command harnessCLIGrammarCommand) bool {
	for _, usage := range command.Usage {
		rest := strings.TrimSpace(strings.TrimPrefix(usage, "scenery "+command.Command))
		if strings.HasPrefix(rest, "<") || strings.HasPrefix(rest, "[<") {
			return true
		}
	}
	return false
}

type harnessCLIGrammarUsage struct {
	paths       [][]string          // command words, one per alternative
	required    []string            // required operands and flags with placeholder values
	valueFlags  []string            // flags that take a value
	choiceFlags map[string][]string // flags whose value is one of a closed set
	outputs     []string            // advertised -o modes
	passthrough bool                // the line forwards arbitrary arguments
}

// harnessCLIGrammarParseUsage reads one usage line: "scenery", the command
// words (where "a|b" are alternatives), then operands and flags, optional parts
// in brackets.
func harnessCLIGrammarParseUsage(usage string) harnessCLIGrammarUsage {
	line := harnessCLIGrammarUsage{choiceFlags: map[string][]string{}}
	if strings.Contains(usage, "...") || strings.Contains(usage, " -- ") {
		line.passthrough = true
		return line
	}
	tokens := strings.Fields(strings.TrimPrefix(usage, "scenery "))
	paths := [][]string{{}}
	index := 0
	for ; index < len(tokens); index++ {
		token := tokens[index]
		if strings.ContainsAny(token, "[<-") {
			break
		}
		var next [][]string
		for _, path := range paths {
			for _, word := range strings.Split(token, "|") {
				next = append(next, append(slices.Clone(path), word))
			}
		}
		paths = next
	}
	line.paths = paths
	depth := 0
	for ; index < len(tokens); index++ {
		token := tokens[index]
		optional := depth > 0 || strings.HasPrefix(token, "[")
		depth += strings.Count(token, "[") - strings.Count(token, "]")
		bare := strings.Trim(token, "[]")
		switch {
		case bare == "-o" && index+1 < len(tokens):
			index++
			depth += strings.Count(tokens[index], "[") - strings.Count(tokens[index], "]")
			modes := strings.Split(strings.Trim(tokens[index], "[]"), "|")
			// "-o json|-o jsonl" spells each alternative with its flag.
			for modes[len(modes)-1] == "-o" && index+1 < len(tokens) {
				index++
				depth += strings.Count(tokens[index], "[") - strings.Count(tokens[index], "]")
				modes = append(modes[:len(modes)-1], strings.Split(strings.Trim(tokens[index], "[]"), "|")...)
			}
			for _, mode := range modes {
				if mode == "json" || mode == "jsonl" {
					line.outputs = append(line.outputs, mode)
				}
			}
		case strings.HasPrefix(bare, "--") && !strings.Contains(bare, "|") && !strings.Contains(bare, "="):
			if index+1 >= len(tokens) || strings.HasPrefix(strings.Trim(tokens[index+1], "[]"), "-") {
				continue
			}
			value := strings.Trim(tokens[index+1], "[]")
			if value == "|" || (!strings.HasPrefix(value, "<") && !strings.Contains(value, "|")) {
				continue
			}
			index++
			depth += strings.Count(tokens[index], "[") - strings.Count(tokens[index], "]")
			line.valueFlags = append(line.valueFlags, bare)
			if !strings.HasPrefix(value, "<") {
				line.choiceFlags[bare] = strings.Split(value, "|")
			}
			if !optional {
				placeholder := "zzvalue"
				if choices := line.choiceFlags[bare]; len(choices) > 0 {
					placeholder = choices[0]
				}
				line.required = append(line.required, bare, placeholder)
			}
		case strings.HasPrefix(bare, "<") && !optional:
			line.required = append(line.required, "zzvalue")
		}
	}
	return line
}

// copyHarnessCLIGrammarFixture copies the authored fixture without the local
// state a developer's own commands left in it.
func copyHarnessCLIGrammarFixture(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".scenery" || entry.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(destination, rel), 0o700)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		return copyHarnessFile(path, filepath.Join(destination, rel), 0o600)
	})
}
