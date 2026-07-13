package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"scenery.sh/internal/build"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/deployplan"
)

func runDeployment(stdout io.Writer, args []string) error {
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
	root, err := findContractRoot(appRoot)
	if err != nil {
		return err
	}
	// Deployment providers are linked explicitly. The core binary must not
	// report success for managed infrastructure it cannot actually provision.
	providers := deployplan.DeploymentProviderRegistry{}
	switch subcommand {
	case "plan":
		if len(positionals) != 1 || strings.TrimSpace(outPath) == "" {
			return fmt.Errorf("usage: scenery deploy plan DEPLOYMENT --out PLAN [-o human|json]")
		}
		result, err := compiler.Compile(root)
		if err != nil {
			return err
		}
		if baseWorkspace == "" {
			baseWorkspace = result.WorkspaceRevision
		}
		if baseContract == "" && result.Manifest != nil {
			baseContract = result.Manifest.ContractRevision
		}
		buildTarget, err := compiler.ResolveGoBuildTarget(result, "", "development")
		if err != nil {
			return err
		}
		bundle, err := build.ReadRuntimeBundle(root, buildTarget.Name)
		if err != nil {
			return fmt.Errorf("failed_precondition: build the selected Go target before deployment planning: %w", err)
		}
		if bundle.ContractRevision != baseContract {
			return fmt.Errorf("revision_conflict: runtime bundle contract_revision changed")
		}
		plan, err := deployplan.PlanDeployment(context.Background(), root, deployplan.DeploymentPlanRequest{
			Deployment: positionals[0], BaseWorkspaceRevision: baseWorkspace, BaseContractRevision: baseContract,
			ImplementationRevisions: map[string]string{bundle.Target: bundle.ImplementationRevision}, Caller: caller, Capabilities: []string{"scenery.deployment"},
		}, providers)
		if err != nil {
			return err
		}
		encoded, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			return err
		}
		if err := writeCLIFile(outPath, append(encoded, '\n')); err != nil {
			return err
		}
		if output == "json" {
			envelope := newCLIEnvelope(true, plan, nil)
			envelope.WorkspaceRevision = plan.BaseWorkspaceRevision
			envelope.ContractRevision = plan.ContractRevision
			envelope.ImplementationRevision = plan.ImplementationRevision
			envelope.DeploymentRevision = plan.DeploymentRevision
			return json.NewEncoder(stdout).Encode(envelope)
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
		var plan deployplan.DeploymentPlan
		if err := readExactPlanFile(planPath, "deployment plan", &plan); err != nil {
			return err
		}
		approvalTokens, err := readApprovalTokens(approvalTokenPaths)
		if err != nil {
			return err
		}
		approvalVerifier, err := approvalVerifierForTokens(root, approvalTokens)
		if err != nil {
			return err
		}
		currentImplementation := map[string]string{}
		for target := range plan.ImplementationRevision {
			bundle, err := build.ReadRuntimeBundle(root, target)
			if err != nil {
				return fmt.Errorf("failed_precondition: selected runtime bundle is unavailable: %w", err)
			}
			if bundle.ContractRevision != plan.ContractRevision {
				return fmt.Errorf("revision_conflict: runtime bundle contract_revision changed")
			}
			currentImplementation[target] = bundle.ImplementationRevision
		}
		receipt, err := deployplan.ApplyDeploymentPlan(context.Background(), root, plan, deployplan.DeploymentApplyOptions{
			ExpectedWorkspaceRevision: expectedWorkspace, ExpectedContractRevision: expectedContract,
			ExpectedImplementation: currentImplementation, Caller: caller, ApprovalTokens: approvalTokens, VerifyApproval: approvalVerifier,
		}, providers)
		if err != nil {
			return err
		}
		if output == "json" {
			envelope := newCLIEnvelope(true, receipt, nil)
			envelope.WorkspaceRevision = receipt.WorkspaceRevision
			envelope.ContractRevision = receipt.ContractRevision
			envelope.ImplementationRevision = receipt.ImplementationRevision
			envelope.DeploymentRevision = receipt.DeploymentRevision
			return json.NewEncoder(stdout).Encode(envelope)
		}
		if !quiet {
			_, err = fmt.Fprintln(stdout, receipt.PlanID)
		}
		return err
	default:
		return fmt.Errorf("unknown scenery deploy subcommand %q", subcommand)
	}
}

func writeCLIFile(path string, data []byte) error {
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
