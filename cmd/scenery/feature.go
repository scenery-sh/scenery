package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"scenery.sh/internal/feature"
)

var featureActions = [...]string{"create", "register", "set", "list", "prepare", "inspect", "check", "land", "close"}

type featureOptions struct {
	Action         string
	Names          []string
	RepoRoot       string
	Path           string
	Purpose        string
	Stage          string
	Dependencies   []string
	NoDependencies bool
	Candidate      string
	Commit         string
	Expected       string
	Yes            bool
	Watch          bool
	Interval       time.Duration
	Output         string
}

type featureResponse struct {
	cliPayloadIdentity
	OK        bool                   `json:"ok"`
	Action    string                 `json:"action"`
	Overview  *feature.Overview      `json:"overview,omitempty"`
	Feature   *feature.Record        `json:"feature,omitempty"`
	Candidate *feature.Candidate     `json:"candidate,omitempty"`
	Receipts  []feature.CheckReceipt `json:"receipts,omitempty"`
}

func featureCommand(args []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return runFeatureCommand(ctx, os.Stdout, args)
}

func parseFeatureArgs(args []string) (featureOptions, error) {
	options := featureOptions{Interval: 2 * time.Second, Output: "human"}
	flags := newCLIFlagSet("feature")
	flags.StringVar(&options.RepoRoot, "repo-root", "", "")
	flags.StringVar(&options.Path, "path", "", "")
	flags.StringVar(&options.Purpose, "purpose", "", "")
	flags.StringVar(&options.Stage, "stage", "", "")
	flags.Func("depends-on", "", func(value string) error { options.Dependencies = append(options.Dependencies, value); return nil })
	flags.BoolVar(&options.NoDependencies, "no-dependencies", false, "")
	flags.StringVar(&options.Candidate, "candidate", "", "")
	flags.StringVar(&options.Commit, "commit", "", "")
	flags.StringVar(&options.Expected, "expect-revision", "", "")
	flags.BoolVar(&options.Yes, "yes", false, "")
	flags.BoolVar(&options.Watch, "watch", false, "")
	flags.DurationVar(&options.Interval, "interval", 2*time.Second, "")
	flags.StringVar(&options.Output, "o", "human", "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return options, err
	}
	if len(positionals) == 0 {
		return options, usageErrorf("usage: scenery feature create|register|set|list|prepare|inspect|check|land|close")
	}
	options.Action, options.Names = positionals[0], positionals[1:]
	if options.Output != "human" && options.Output != "json" && options.Output != "jsonl" {
		return options, usageErrorf("unsupported output %q", options.Output)
	}
	if options.Output == "jsonl" && (options.Action != "list" || !options.Watch) {
		return options, usageErrorf("-o jsonl requires feature list --watch")
	}
	if options.Watch && options.Output == "json" {
		return options, usageErrorf("feature list --watch requires -o human or jsonl")
	}
	if options.Watch && options.Action != "list" {
		return options, usageErrorf("--watch belongs only to feature list")
	}
	if options.Interval < 100*time.Millisecond {
		return options, usageErrorf("--interval must be at least 100ms")
	}
	if options.Yes || options.Expected != "" {
		if options.Action != "land" && options.Action != "check" {
			return options, usageErrorf("candidate approval belongs only to feature land or check")
		}
		if options.Candidate == "" || options.Expected == "" {
			return options, usageErrorf("candidate application requires --candidate and --expect-revision")
		}
	}
	if options.Action == "land" && options.Candidate != "" && !options.Yes {
		return options, usageErrorf("landing a candidate requires --yes and --expect-revision")
	}
	if options.NoDependencies && len(options.Dependencies) > 0 {
		return options, usageErrorf("--no-dependencies conflicts with --depends-on")
	}
	switch options.Action {
	case "create", "register", "set", "inspect", "close":
		if len(options.Names) != 1 {
			return options, usageErrorf("feature %s requires one name", options.Action)
		}
	case "list":
		if len(options.Names) > 0 {
			return options, usageErrorf("unexpected argument %q", options.Names[0])
		}
	case "prepare", "land":
		if options.Candidate == "" && len(options.Names) == 0 {
			return options, usageErrorf("feature %s requires a feature name", options.Action)
		}
		if options.Candidate != "" && len(options.Names) > 0 {
			return options, usageErrorf("--candidate conflicts with feature names")
		}
		if options.Commit != "" && len(options.Names) != 1 {
			return options, usageErrorf("--commit selects exactly one feature")
		}
	case "check":
		if options.Candidate == "" && len(options.Names) != 1 {
			return options, usageErrorf("feature check requires a name or an approved candidate")
		}
		if options.Candidate != "" && (len(options.Names) > 0 || options.Expected == "") {
			return options, usageErrorf("candidate check requires --expect-revision and no feature name")
		}
	default:
		return options, usageErrorf("unknown feature command %q", options.Action)
	}
	if (options.Action == "create" || options.Action == "register") && options.Purpose == "" {
		return options, usageErrorf("feature %s requires --purpose", options.Action)
	}
	if options.Action == "register" && options.Path == "" {
		return options, usageErrorf("feature register requires --path")
	}
	if options.Candidate != "" && options.Action != "land" && options.Action != "check" {
		return options, usageErrorf("--candidate belongs only to feature land or check")
	}
	if options.Commit != "" && options.Action != "prepare" && options.Action != "land" {
		return options, usageErrorf("--commit belongs only to feature prepare or land")
	}
	if (options.Stage != "" || options.NoDependencies) && options.Action != "set" {
		return options, usageErrorf("--stage and --no-dependencies belong only to feature set")
	}
	if (options.Purpose != "" || len(options.Dependencies) > 0) && options.Action != "create" && options.Action != "register" && options.Action != "set" {
		return options, usageErrorf("feature metadata belongs only to create, register or set")
	}
	if options.Path != "" && options.Action != "create" && options.Action != "register" {
		return options, usageErrorf("--path belongs only to create or register")
	}
	return options, nil
}

func runFeatureCommand(ctx context.Context, stdout io.Writer, args []string) error {
	options, err := parseFeatureArgs(args)
	if err != nil {
		return err
	}
	manager, err := feature.Open(ctx, options.RepoRoot)
	if err != nil {
		return preconditionErrorf("%v", err)
	}
	response := featureResponse{cliPayloadIdentity: newCLIPayloadIdentity("scenery.feature"), OK: true, Action: options.Action}
	var record feature.Record
	var candidate feature.Candidate
	switch options.Action {
	case "create":
		record, err = manager.Create(ctx, options.Names[0], options.Purpose, options.Path, options.Dependencies)
		response.Feature = &record
	case "register":
		record, err = manager.Register(ctx, options.Names[0], options.Purpose, options.Path, options.Dependencies)
		response.Feature = &record
	case "set":
		record, err = manager.Update(ctx, options.Names[0], feature.Update{Purpose: options.Purpose, Stage: options.Stage, Dependencies: options.Dependencies, SetDependencies: options.NoDependencies || len(options.Dependencies) > 0})
		response.Feature = &record
	case "close":
		record, err = manager.Close(ctx, options.Names[0])
		response.Feature = &record
	case "prepare":
		candidate, err = manager.Prepare(ctx, options.Names, options.Commit)
		response.Candidate = &candidate
	case "inspect":
		candidate, err = manager.Inspect(ctx, options.Names[0])
		response.Candidate = &candidate
	case "land":
		if options.Candidate == "" {
			candidate, err = manager.Prepare(ctx, options.Names, options.Commit)
		} else {
			candidate, err = manager.Land(ctx, options.Candidate, options.Expected)
		}
		response.Candidate = &candidate
	case "check":
		if options.Candidate != "" {
			candidate, err = manager.CheckCandidate(ctx, options.Candidate, options.Expected)
			response.Candidate = &candidate
		} else {
			response.Receipts, err = manager.CheckDevelopment(ctx, options.Names[0])
		}
	case "list":
		return watchFeatures(ctx, stdout, manager, options)
	}
	if err != nil {
		return preconditionErrorf("%v", err)
	}
	return writeFeatureResponse(stdout, response, options.Output)
}

func watchFeatures(ctx context.Context, stdout io.Writer, manager *feature.Manager, options featureOptions) (returnErr error) {
	previous := ""
	var events *cliEventWriter
	count := 0
	if options.Output == "jsonl" {
		events = newCLIEventWriter(stdout)
		defer func() {
			data := map[string]any{"event_count": count}
			if returnErr != nil {
				data["ok"] = false
				data["diagnostic"] = cliErrorDiagnostic(returnErr)
			}
			if err := events.write("summary", true, data); err != nil {
				returnErr = &silentCLIError{err: fmt.Errorf("encode feature summary: %w", err), code: 10}
			} else if returnErr != nil {
				returnErr = &silentCLIError{err: returnErr, code: cliExitCode(returnErr)}
			}
		}()
	}
	for {
		overview, err := manager.List(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return preconditionErrorf("%v", err)
		}
		for i := range overview.Features {
			row := &overview.Features[i]
			if _, err := os.Stat(filepath.Join(row.Path, ".scenery.json")); os.IsNotExist(err) {
				row.Runtime = "not_app"
				continue
			}
			entries, err := inspectWorktreeOwners(ctx, row.Path)
			if err != nil || len(entries) != 1 {
				row.Runtime = "unavailable"
				continue
			}
			row.Runtime = entries[0].Status
		}
		encoded, err := json.Marshal(overview)
		if err != nil {
			return err
		}
		if string(encoded) != previous {
			response := featureResponse{cliPayloadIdentity: newCLIPayloadIdentity("scenery.feature"), OK: true, Action: "list", Overview: &overview}
			if events != nil {
				if err := events.event(response); err != nil {
					return err
				}
				count++
			} else if err := writeFeatureResponse(stdout, response, options.Output); err != nil {
				return err
			}
			previous = string(encoded)
		}
		if !options.Watch {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(options.Interval):
		}
	}
}

func writeFeatureResponse(stdout io.Writer, response featureResponse, output string) error {
	if output != "human" {
		return writeCLIJSON(stdout, response)
	}
	if response.Overview != nil {
		writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(writer, "FEATURE\tSTATUS\tSTAGE\tPURPOSE\tDEPENDENCIES\tOUTSTANDING\tOVERLAP\tVALIDATION\tLANDING\tRUNTIME")
		for _, row := range response.Overview.Features {
			overlap := []string{}
			for _, item := range row.Overlap {
				overlap = append(overlap, item.Feature+":"+strings.Join(item.Paths, ","))
			}
			_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", row.Name, row.Status, row.Stage, strings.ReplaceAll(row.Purpose, "\t", " "), strings.Join(row.Dependencies, ","), strings.Join(row.Outstanding, ","), strings.Join(overlap, ";"), row.Validation, row.Landing, row.Runtime)
			if len(row.Blockers) > 0 {
				_, _ = fmt.Fprintf(writer, "  %s\n", strings.Join(row.Blockers, "; "))
			}
		}
		return writer.Flush()
	}
	if response.Feature != nil {
		_, err := fmt.Fprintf(stdout, "%s: %s (%s) at %s\n", response.Feature.Name, response.Feature.Purpose, response.Feature.Stage, response.Feature.Path)
		return err
	}
	if response.Candidate != nil {
		c := response.Candidate
		_, _ = fmt.Fprintf(stdout, "candidate %s: %s\ncheckout: %s\nmain base: %s\nrevision: %s\n%s\n", c.ID, c.Status, c.Path, c.Base, c.Revision, c.Diff)
		if len(c.Conflicts) > 0 {
			_, _ = fmt.Fprintf(stdout, "conflicts: %s\nresolve and stage in the candidate, then run scenery feature inspect %s\n", strings.Join(c.Conflicts, ", "), c.ID)
		} else if c.Status != "published" {
			_, _ = fmt.Fprintf(stdout, "review: git -C %q diff %s HEAD\nland: scenery feature land --candidate %s --yes --expect-revision %s\n", c.Path, c.Base, c.ID, c.Revision)
		}
		return nil
	}
	for _, receipt := range response.Receipts {
		_, _ = fmt.Fprintf(stdout, "%s: %s (%dms) %s\n", receipt.ID, receipt.Outcome, receipt.DurationMS, receipt.Archive)
	}
	return nil
}
