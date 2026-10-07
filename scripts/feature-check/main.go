// Command feature-check completes Scenery's source checks for a combined Git
// candidate. External probes remain separate policy checks with shared admission.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"

	"scenery.sh/internal/graph"
	"scenery.sh/internal/harnessreport"
	"scenery.sh/internal/machine"
	"scenery.sh/internal/repoinfo"
	"scenery.sh/internal/spec"
)

type contextSummary struct {
	Run              *harnessreport.ValidationRun `json:"run"`
	RepoChecksPassed bool                         `json:"repo_checks_passed"`
	FullContextPath  string                       `json:"full_context_path"`
	Checks           []contextCheck               `json:"checks"`
}
type contextCheck struct {
	Command string `json:"command"`
	Status  string `json:"status"`
}
type fullContext struct {
	Commands []string `json:"changed_area_recommended_commands"`
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("feature-check", flag.ContinueOnError)
	base := flags.String("base", "", "exact integration base commit")
	development := flags.Bool("development", false, "focused development feedback only")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	if *base == "" && !*development {
		return errors.New("landing checks require --base")
	}
	changes, err := changedFiles(ctx, *base)
	if err != nil {
		return err
	}
	mode := selectedMode(changes, *development)
	command := []string{"go", "run", "./scripts/verify", "--summary", "-o", "json", "--write"}
	if mode == "quick" {
		command = append(command, "--quick")
	}
	if *base != "" {
		command = append(command, "--base", *base)
	}
	output, runErr := capture(ctx, command)
	report, err := decodeVerifierSummary(output)
	if runErr != nil || err != nil || !report.OK {
		if report.Run != nil && report.Run.ArchivePath != "" {
			return fmt.Errorf("repository verifier failed; inspect %s/self.json: %w", report.Run.ArchivePath, errors.Join(runErr, err))
		}
		if len(output) > 4000 {
			output = output[len(output)-4000:]
		}
		return fmt.Errorf("repository verifier: %w: %s", errors.Join(runErr, err), output)
	}
	if report.Run == nil || report.Run.ArchivePath == "" {
		return errors.New("verifier did not archive evidence")
	}
	// Read compact context first; only its exact immutable run selects work.
	var summary contextSummary
	if err := readJSON(filepath.Join(report.Run.ArchivePath, "agent-context-summary.json"), &summary); err != nil {
		return err
	}
	if !summary.RepoChecksPassed || summary.Run == nil || *summary.Run != *report.Run {
		return errors.New("verifier context does not certify stable current inputs")
	}
	if *development {
		fmt.Printf("Focused feedback passed: %s\n", report.Run.ArchivePath)
		return nil
	}
	var full fullContext
	if err := readJSON(summary.FullContextPath, &full); err != nil {
		return err
	}
	remaining, err := remainingCommands(summary, full.Commands)
	if err != nil {
		return err
	}
	for _, command := range remaining {
		argv, err := commandArgs(command)
		if err != nil {
			return err
		}
		fmt.Println("Required check:", command)
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s: %w", command, err)
		}
	}
	fmt.Printf("Combined source checks passed: %s\n", report.Run.ArchivePath)
	return nil
}

func capture(ctx context.Context, args []string) ([]byte, error) {
	// Keep machine stdout intact when go run reports a failed child on stderr.
	output, err := exec.CommandContext(ctx, args[0], args[1:]...).Output()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
		stderr := strings.TrimSpace(string(exitErr.Stderr))
		if len(stderr) > 4000 {
			stderr = stderr[len(stderr)-4000:]
		}
		err = fmt.Errorf("%w: %s", err, stderr)
	}
	return output, err
}
func readJSON(path string, value any) error {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(bytes, value)
}

func changedFiles(ctx context.Context, base string) ([]harnessreport.ChangedFile, error) {
	commands := [][]string{{"git", "diff", "--no-renames", "--name-only", "-z", "HEAD", "--"}, {"git", "ls-files", "--others", "--exclude-standard", "-z"}}
	if base != "" {
		commands = append(commands, []string{"git", "diff", "--no-renames", "--name-only", "-z", base, "HEAD", "--"})
	}
	changes := []harnessreport.ChangedFile{}
	seen := map[string]bool{}
	for _, command := range commands {
		output, err := capture(ctx, command)
		if err != nil {
			return nil, fmt.Errorf("change scope: %w: %s", err, output)
		}
		for _, path := range strings.Split(string(output), "\x00") {
			if path != "" && !seen[path] {
				changes = append(changes, harnessreport.ChangedFile{Path: path, Status: "modified"})
				seen[path] = true
			}
		}
	}
	return changes, nil
}

func selectedMode(changes []harnessreport.ChangedFile, development bool) string {
	if development {
		return "quick"
	}
	var report harnessreport.ChangedAreaReport
	repoinfo.PopulateChangedArea(".", &report, changes, nil, nil)
	if len(report.ChangedFiles) == 0 || slices.Equal(report.ValidationClasses, []string{repoinfo.ValidationDocumentation}) {
		return "quick"
	}
	return "default"
}

func remainingCommands(summary contextSummary, commands []string) ([]string, error) {
	result := []string{}
	for _, command := range append(slices.Clone(commands), "golangci-lint run ./...") {
		if slices.Contains(result, command) {
			continue
		}
		status := "remaining"
		for _, check := range summary.Checks {
			if check.Command == command {
				status = check.Status
				break
			}
		}
		if status == "covered" {
			continue
		}
		if status == "conditional" {
			return nil, fmt.Errorf("required owner acceptance remains: %s; record that scenario before landing", command)
		}
		result = append(result, command)
	}
	return result, nil
}

func commandArgs(command string) ([]string, error) {
	// The classifier emits repository-owned simple argv. Fail closed if that
	// contract changes; do not interpret new strings through a shell.
	if strings.ContainsAny(command, "\n\r\"'`;|&<>$") {
		return nil, fmt.Errorf("required command needs explicit argv support: %s", command)
	}
	args := strings.Fields(command)
	if len(args) == 0 {
		return nil, errors.New("empty required command")
	}
	if len(args) >= 3 && args[0] == "go" && args[1] == "run" && args[2] == "./cmd/scenery" {
		args = append([]string{".scenery/harness/bin/scenery"}, args[3:]...)
	}
	return args, nil
}

func decodeVerifierSummary(output []byte) (harnessreport.SelfSummaryResponse, error) {
	var report harnessreport.SelfSummaryResponse
	if err := machine.DecodeData[graph.Diagnostic](output, string(spec.CurrentRevision()), &report); err != nil {
		return report, err
	}
	if report.PayloadIdentity != machine.NewPayloadIdentity("scenery.harness.self.summary") {
		return report, errors.New("verifier returned an unexpected or failing summary identity")
	}
	return report, nil
}
