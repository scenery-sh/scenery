package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"scenery.sh/internal/app"
	"scenery.sh/internal/envpolicy"
	sceneryruntime "scenery.sh/runtime"
)

const temporalDeploymentTimeout = 30 * time.Second

type temporalDeploymentOptions struct {
	AppRoot                 string
	Deployment              string
	BuildID                 string
	Percentage              float64
	PercentageSet           bool
	IgnoreMissingTaskQueues bool
	AllowNoPollers          bool
	Force                   bool
	JSON                    bool
}

type temporalDeploymentResult struct {
	OK         bool    `json:"ok"`
	Action     string  `json:"action"`
	Deployment string  `json:"deployment"`
	BuildID    string  `json:"build_id,omitempty"`
	Percentage float64 `json:"percentage,omitempty"`
	Namespace  string  `json:"namespace"`
	Address    string  `json:"address"`
}

func temporalDeploymentCommand(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: scenery worker deployment set-current|ramp|drain [flags]")
	}
	action := args[0]
	switch action {
	case "set-current", "ramp", "drain":
	default:
		return fmt.Errorf("unknown temporal deployment command %q", action)
	}
	opts, err := parseTemporalDeploymentArgs(action, args[1:])
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), temporalDeploymentTimeout)
	defer cancel()
	return runTemporalDeployment(ctx, action, opts, stdout)
}

func parseTemporalDeploymentArgs(action string, args []string) (temporalDeploymentOptions, error) {
	var opts temporalDeploymentOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--app-root":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--deployment":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("missing value for --deployment")
			}
			opts.Deployment = strings.TrimSpace(args[i])
			if opts.Deployment == "" {
				return opts, fmt.Errorf("--deployment must not be empty")
			}
		case "--build-id":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("missing value for --build-id")
			}
			opts.BuildID = strings.TrimSpace(args[i])
			if opts.BuildID == "" {
				return opts, fmt.Errorf("--build-id must not be empty")
			}
		case "--percentage":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("missing value for --percentage")
			}
			value, err := strconv.ParseFloat(args[i], 64)
			if err != nil {
				return opts, fmt.Errorf("invalid --percentage %q", args[i])
			}
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return opts, fmt.Errorf("invalid --percentage %q", args[i])
			}
			opts.Percentage = value
			opts.PercentageSet = true
		case "--ignore-missing-task-queues":
			opts.IgnoreMissingTaskQueues = true
		case "--allow-no-pollers":
			opts.AllowNoPollers = true
		case "--force":
			opts.Force = true
		case "--json":
			opts.JSON = true
		default:
			return opts, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	switch action {
	case "set-current", "ramp", "drain":
		if opts.BuildID == "" {
			return opts, fmt.Errorf("%s requires --build-id", action)
		}
	}
	if action == "ramp" {
		if !opts.PercentageSet {
			return opts, fmt.Errorf("ramp requires --percentage")
		}
		if opts.Percentage < 0 || opts.Percentage > 100 {
			return opts, fmt.Errorf("--percentage must be between 0 and 100")
		}
	} else if opts.PercentageSet {
		return opts, fmt.Errorf("--percentage is only valid with ramp")
	}
	if opts.Force && action != "drain" {
		return opts, fmt.Errorf("--force is only valid with drain")
	}
	if action == "drain" {
		if opts.IgnoreMissingTaskQueues {
			return opts, fmt.Errorf("--ignore-missing-task-queues is not valid with drain")
		}
		if opts.AllowNoPollers {
			return opts, fmt.Errorf("--allow-no-pollers is not valid with drain")
		}
	}
	return opts, nil
}

func runTemporalDeployment(ctx context.Context, action string, opts temporalDeploymentOptions, stdout io.Writer) error {
	root, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	root, cfg, err := app.DiscoverRoot(root)
	if err != nil {
		return err
	}
	env, err := appEnvWithDotEnv(envpolicy.Environ(), root, ".env", ".env.local")
	if err != nil {
		return err
	}
	restoreEnv := applyTemporaryEnv(envListMap(env))
	defer restoreEnv()

	rtCfg := temporalRuntimeConfigFromApp(cfg.Temporal)
	if !rtCfg.Enabled {
		return fmt.Errorf("temporal deployment commands require temporal.enabled=true")
	}
	info := sceneryruntime.ResolveTemporalConfig(cfg.Name, rtCfg)
	if opts.Deployment != "" {
		info.DeploymentName = opts.Deployment
	}
	temporalCLI, err := resolveTemporalCLI(ctx, temporalToolchainStoreDir(root), true)
	if err != nil {
		return err
	}
	if err := runTemporalDeploymentCLI(ctx, action, opts, info, temporalCLI); err != nil {
		return err
	}
	result := temporalDeploymentResult{
		OK:         true,
		Action:     action,
		Deployment: sceneryruntime.TemporalDeploymentName(info),
		BuildID:    opts.BuildID,
		Percentage: opts.Percentage,
		Namespace:  info.Namespace,
		Address:    info.Address,
	}
	if opts.JSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}
	if _, err := fmt.Fprintf(stdout, "worker deployment %s applied to %s build %s\n", action, result.Deployment, result.BuildID); err != nil {
		return err
	}
	return nil
}

func runTemporalDeploymentCLI(ctx context.Context, action string, opts temporalDeploymentOptions, info sceneryruntime.TemporalRuntimeInfo, path string) error {
	args := temporalDeploymentCLIArgs(action, opts, info)
	cmd := exec.CommandContext(ctx, path, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("temporal deployment %s %s build %s: %w\n%s", action, sceneryruntime.TemporalDeploymentName(info), opts.BuildID, err, output)
	}
	return nil
}

func temporalDeploymentCLIArgs(action string, opts temporalDeploymentOptions, info sceneryruntime.TemporalRuntimeInfo) []string {
	deployment := sceneryruntime.TemporalDeploymentName(info)
	var args []string
	switch action {
	case "set-current":
		args = append(args,
			"worker", "deployment", "set-current-version",
			"--deployment-name", deployment,
			"--build-id", opts.BuildID,
			"--identity", "scenery-cli",
			"--yes",
		)
		if opts.IgnoreMissingTaskQueues {
			args = append(args, "--ignore-missing-task-queues")
		}
		if opts.AllowNoPollers {
			args = append(args, "--allow-no-pollers")
		}
	case "ramp":
		args = append(args,
			"worker", "deployment", "set-ramping-version",
			"--deployment-name", deployment,
			"--build-id", opts.BuildID,
			"--percentage", strconv.FormatFloat(opts.Percentage, 'f', -1, 64),
			"--identity", "scenery-cli",
			"--yes",
		)
		if opts.IgnoreMissingTaskQueues {
			args = append(args, "--ignore-missing-task-queues")
		}
		if opts.AllowNoPollers {
			args = append(args, "--allow-no-pollers")
		}
	case "drain":
		args = append(args,
			"worker", "deployment", "delete-version",
			"--deployment-name", deployment,
			"--build-id", opts.BuildID,
			"--identity", "scenery-cli",
		)
		if opts.Force {
			args = append(args, "--skip-drainage")
		}
	}
	args = append(args,
		"--address", info.Address,
		"--namespace", info.Namespace,
		"--command-timeout", temporalDeploymentTimeout.String(),
		"--client-connect-timeout", sceneryruntime.DefaultTemporalConnectWait.String(),
		"--color", "never",
		"--output", "json",
	)
	if info.APIKeyEnvSet {
		if value := strings.TrimSpace(envpolicy.Get(info.APIKeyEnv)); value != "" {
			args = append(args, "--api-key", value)
		}
	}
	if info.TLSEnabled {
		args = append(args, "--tls")
	}
	if info.TLSServerNameSet {
		args = append(args, "--tls-server-name", info.TLSServerName)
	}
	if info.TLSCACertFileSet {
		if value := strings.TrimSpace(envpolicy.Get(info.TLSCACertFileEnv)); value != "" {
			args = append(args, "--tls-ca-path", value)
		}
	}
	if info.TLSCertFileSet {
		if value := strings.TrimSpace(envpolicy.Get(info.TLSCertFileEnv)); value != "" {
			args = append(args, "--tls-cert-path", value)
		}
	}
	if info.TLSKeyFileSet {
		if value := strings.TrimSpace(envpolicy.Get(info.TLSKeyFileEnv)); value != "" {
			args = append(args, "--tls-key-path", value)
		}
	}
	return args
}

func envListMap(env []string) map[string]string {
	values := make(map[string]string, len(env))
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		values[key] = value
	}
	return values
}
