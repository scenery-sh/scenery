package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"scenery.sh/internal/build"
	"scenery.sh/internal/vnext"
)

func runVNextDeploy(stdout io.Writer, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: scenery deploy plan|apply")
	}
	subcommand := args[0]
	var appRoot, output, outPath, planPath, baseWorkspace, baseContract, expectedWorkspace, expectedContract, caller string
	var approvalTokenPaths []string
	var quiet, nonInteractive bool
	flags := newCLIFlagSet("deploy " + subcommand)
	flags.StringVar(&appRoot, "app-root", "", "")
	flags.StringVar(&output, "o", "human", "")
	flags.StringVar(&outPath, "out", "", "")
	flags.StringVar(&planPath, "plan", "", "")
	flags.StringVar(&baseWorkspace, "base-workspace-revision", "", "")
	flags.StringVar(&baseContract, "base-contract-revision", "", "")
	flags.StringVar(&expectedWorkspace, "expect-workspace-revision", "", "")
	flags.StringVar(&expectedContract, "expect-contract-revision", "", "")
	flags.StringVar(&caller, "caller", "local", "")
	flags.Func("approval-token", "", func(value string) error { approvalTokenPaths = append(approvalTokenPaths, value); return nil })
	flags.BoolVar(&quiet, "quiet", false, "")
	flags.BoolVar(&nonInteractive, "non-interactive", false, "")
	positionals, err := parseCLIFlags(flags, args[1:])
	if err != nil {
		return err
	}
	if output != "human" && output != "json" {
		return fmt.Errorf("unsupported output %q", output)
	}
	root, err := vnextRoot(appRoot)
	if err != nil {
		return err
	}
	// Deployment providers are linked explicitly. The core binary must not
	// report success for managed infrastructure it cannot actually provision.
	providers := vnext.DeploymentProviderRegistry{}
	switch subcommand {
	case "plan":
		if len(positionals) != 1 || strings.TrimSpace(outPath) == "" {
			return fmt.Errorf("usage: scenery deploy plan DEPLOYMENT --out PLAN [-o human|json]")
		}
		result, err := vnext.Compile(root)
		if err != nil {
			return err
		}
		if baseWorkspace == "" {
			baseWorkspace = result.WorkspaceRevision
		}
		if baseContract == "" && result.Manifest != nil {
			baseContract = result.Manifest.ContractRevision
		}
		buildTarget, err := vnext.ResolveGoBuildTarget(result, "", "development")
		if err != nil {
			return err
		}
		bundle, err := build.ReadVNextRuntimeBundle(root, buildTarget.Name)
		if err != nil {
			return fmt.Errorf("failed_precondition: build the selected Go target before deployment planning: %w", err)
		}
		if bundle.ContractRevision != baseContract {
			return fmt.Errorf("revision_conflict: runtime bundle contract_revision changed")
		}
		plan, err := vnext.PlanDeployment(context.Background(), root, vnext.DeploymentPlanRequest{
			Deployment: positionals[0], BaseWorkspaceRevision: baseWorkspace, BaseContractRevision: baseContract,
			ImplementationRevisions: map[string]string{bundle.Target: bundle.ImplementationRevision}, Caller: caller, Capabilities: []string{"scenery.deployment/v1"},
		}, providers)
		if err != nil {
			return err
		}
		encoded, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			return err
		}
		if err := writeVNextCLIFile(outPath, append(encoded, '\n')); err != nil {
			return err
		}
		if output == "json" {
			return json.NewEncoder(stdout).Encode(vnextEnvelope{
				APIVersion: "scenery.cli.v1", DiagnosticCatalog: vnext.DiagnosticCatalog, OK: true,
				WorkspaceRevision: plan.BaseWorkspaceRevision, ContractRevision: plan.ContractRevision,
				ImplementationRevision: plan.ImplementationRevision, DeploymentRevision: plan.DeploymentRevision,
				Data: plan, Diagnostics: []vnext.Diagnostic{},
			})
		}
		if !quiet {
			_, err = fmt.Fprintln(stdout, plan.PlanID)
		}
		return err
	case "apply":
		if planPath == "" && len(positionals) == 1 {
			planPath = positionals[0]
		} else if planPath == "" || len(positionals) != 0 {
			return fmt.Errorf("usage: scenery deploy apply PLAN --expect-workspace-revision REV --expect-contract-revision REV")
		}
		if expectedWorkspace == "" || expectedContract == "" {
			return fmt.Errorf("usage: scenery deploy apply PLAN --expect-workspace-revision REV --expect-contract-revision REV")
		}
		var plan vnext.DeploymentPlan
		if err := readExactVNextPlanFile(planPath, "deployment plan", &plan); err != nil {
			return err
		}
		approvalTokens, err := readMigrationApprovalTokens(approvalTokenPaths)
		if err != nil {
			return err
		}
		approvalVerifier, err := approvalVerifierForTokens(root, approvalTokens)
		if err != nil {
			return err
		}
		currentImplementation := map[string]string{}
		for target := range plan.ImplementationRevision {
			bundle, err := build.ReadVNextRuntimeBundle(root, target)
			if err != nil {
				return fmt.Errorf("failed_precondition: selected runtime bundle is unavailable: %w", err)
			}
			if bundle.ContractRevision != plan.ContractRevision {
				return fmt.Errorf("revision_conflict: runtime bundle contract_revision changed")
			}
			currentImplementation[target] = bundle.ImplementationRevision
		}
		receipt, err := vnext.ApplyDeploymentPlan(context.Background(), root, plan, vnext.DeploymentApplyOptions{
			ExpectedWorkspaceRevision: expectedWorkspace, ExpectedContractRevision: expectedContract,
			ExpectedImplementation: currentImplementation, Caller: caller, ApprovalTokens: approvalTokens, VerifyApproval: approvalVerifier,
		}, providers)
		if err != nil {
			return err
		}
		if output == "json" {
			return json.NewEncoder(stdout).Encode(vnextEnvelope{
				APIVersion: "scenery.cli.v1", DiagnosticCatalog: vnext.DiagnosticCatalog, OK: true,
				WorkspaceRevision: receipt.WorkspaceRevision, ContractRevision: receipt.ContractRevision,
				ImplementationRevision: receipt.ImplementationRevision, DeploymentRevision: receipt.DeploymentRevision,
				Data: receipt, Diagnostics: []vnext.Diagnostic{},
			})
		}
		if !quiet {
			_, err = fmt.Fprintln(stdout, receipt.PlanID)
		}
		return err
	default:
		return fmt.Errorf("unknown scenery deploy subcommand %q", subcommand)
	}
}

func writeVNextCLIFile(path string, data []byte) error {
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}
