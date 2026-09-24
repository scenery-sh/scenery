package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/appconfig"
)

type deploySSHOptions struct {
	AppRoot string
	Env     string
}

func runDeploySSH(stdout io.Writer, target string, args []string, tools deploySSHTools) error {
	opts, err := parseDeploySSHOptions(target, args)
	if err != nil {
		return err
	}
	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	appRoot, cfg, err := appcfg.DiscoverRoot(start)
	if err != nil {
		return err
	}
	var env appcfg.ResolvedEnv
	if opts.Env != "" {
		env, err = resolveDeployEnv(cfg, opts.Env)
		if err != nil {
			return err
		}
		if target == "" {
			if len(env.Deploy.SSH) != 1 {
				return fmt.Errorf("envs.%s.deploy.ssh must contain exactly one target for scenery deploy --env", env.Name)
			}
			target = env.Deploy.SSH[0]
		}
	} else {
		env, err = cfg.EnvForSSHTarget(target)
		if err != nil {
			return usageErrorf("%v; list it in envs.<name>.deploy.ssh of %s", err, appcfg.PrimaryConfigFilename)
		}
	}
	if err := tools.check(context.Background(), stdout, []string{"--app-root", appRoot}); err != nil {
		return fmt.Errorf("local scenery check: %w", err)
	}
	if strings.TrimSpace(cfg.ID) == "" || !appconfig.ValidIdentifier(cfg.ID) {
		return preconditionErrorf("deployment needs an explicit stable lowercase application id; add \"id\": %q to %s", cfg.AppID(), appcfg.PrimaryConfigFilename)
	}
	publishFrontends := strings.TrimSpace(env.Domain) != "" && len(productionFrontendNames(env)) > 0
	return runDeploySSHCommands(stdout, appRoot, cfg.ID, target, env.Name, publishFrontends, tools)
}

// deploySSHTools captures the local check and external-command boundaries.
// The zero value runs the production check and resolves "ssh" and "rsync" from
// PATH; tests can inject either boundary without mutating process-global state.
type deploySSHTools struct {
	SSH        string
	Rsync      string
	Env        []string
	Check      func(context.Context, io.Writer, []string) error
	RunCommand func(string, *exec.Cmd) error
	Exchange   func(string, deployRemoteRequest) (deployRemoteResponse, error)
}

func (t deploySSHTools) check(ctx context.Context, stdout io.Writer, args []string) error {
	if t.Check != nil {
		return t.Check(ctx, stdout, args)
	}
	return runSceneryCheck(ctx, stdout, args)
}

func (t deploySSHTools) ssh() string {
	if strings.TrimSpace(t.SSH) == "" {
		return "ssh"
	}
	return t.SSH
}

func (t deploySSHTools) rsync() string {
	if strings.TrimSpace(t.Rsync) == "" {
		return "rsync"
	}
	return t.Rsync
}

// exchange sends one deployment request to the target's fixed receiver.
func (t deploySSHTools) exchange(target string, request deployRemoteRequest) (deployRemoteResponse, error) {
	if t.Exchange != nil {
		return t.Exchange(target, request)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return deployRemoteResponse{}, err
	}
	var output bytes.Buffer
	command := exec.Command(t.ssh(), "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "--", target, deployRemoteCommand)
	command.Stdin, command.Stdout, command.Stderr = bytes.NewReader(encoded), &output, cliStderr
	if len(t.Env) > 0 {
		command.Env = append(command.Environ(), t.Env...)
	}
	runErr := command.Run()
	var response deployRemoteResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil || response.Kind != deployResponseKind {
		if runErr != nil {
			return response, unavailableErrorf("target %s is unreachable or runs an older Scenery: %v", target, runErr)
		}
		return response, unavailableErrorf("target %s did not answer the deployment protocol; upgrade Scenery on the target", target)
	}
	if response.Protocol != deployProtocolVersion {
		return response, unavailableErrorf("target %s speaks deployment protocol %d, this Scenery speaks %d", target, response.Protocol, deployProtocolVersion)
	}
	return response, nil
}

func (t deploySSHTools) runCommand(name string, cmd *exec.Cmd) error {
	if t.RunCommand != nil {
		return t.RunCommand(name, cmd)
	}
	return cmd.Run()
}

var deployRemotePathPattern = regexp.MustCompile(`^[A-Za-z0-9/_.+-]+/?$`)

// runDeploySSHCommands deploys one release. The healthy runtime keeps
// serving through capture, staging and validation; only activation stops
// it, and a failed activation restores the previous release's exact source
// and configuration.
func runDeploySSHCommands(stdout io.Writer, appRoot, appID, target, envName string, publishFrontends bool, tools deploySSHTools) error {
	identifier := make([]byte, 16)
	if _, err := rand.Read(identifier); err != nil {
		return err
	}
	deploymentID := hex.EncodeToString(identifier)
	run := func(name string, cmd *exec.Cmd) error {
		cmd.Dir = filepath.Clean(appRoot)
		if len(tools.Env) > 0 {
			// Extend the command's own environment rather than the process
			// environment: with a nil Env the exec package derives PWD from
			// Dir, and rsync's logged working directory depends on it. Copying
			// the raw process environment would silently switch the child to
			// getcwd()'s symlink-resolved path.
			cmd.Env = append(cmd.Environ(), tools.Env...)
		}
		cmd.Stdin = os.Stdin
		cmd.Stdout = stdout
		cmd.Stderr = cliStderr
		if err := tools.runCommand(name, cmd); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		return nil
	}
	remote := func(name, command string) error {
		return run(name, exec.Command(tools.ssh(), "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "--", target, command))
	}
	exchange := func(operation string) (deployRemoteResponse, error) {
		response, err := tools.exchange(target, deployRemoteRequest{Kind: deployRequestKind, Protocol: deployProtocolVersion, AppID: appID, Environment: envName, Operation: operation, DeploymentID: deploymentID})
		if err != nil {
			return response, fmt.Errorf("remote deploy %s: %w", operation, err)
		}
		if !response.OK {
			message := response.Error
			if len(response.Problems) > 0 {
				message = strings.Join(response.Problems, "; ")
			}
			return response, preconditionErrorf("remote deploy %s: %s", operation, message)
		}
		return response, nil
	}
	if err := remote("SSH preflight", `command -v scenery >/dev/null && command -v rsync >/dev/null`); err != nil {
		return err
	}
	begun, err := exchange("begin")
	if err != nil {
		return err
	}
	if !deployRemotePathPattern.MatchString(begun.StagingPath) || !deployRemotePathPattern.MatchString(begun.SourceRoot) {
		return preconditionErrorf("remote deploy begin returned an unsupported path")
	}
	_, _ = fmt.Fprintf(stdout, "deployment %s captures configuration revision %s\n", deploymentID, begun.ConfigRevision)
	abort := func(cause error) error {
		if _, abortErr := exchange("abort"); abortErr != nil {
			return errors.Join(cause, abortErr)
		}
		return cause
	}
	if err := run("rsync", exec.Command(tools.rsync(), "-az", "--delete", "--filter=:- .gitignore", "--exclude=.git/", "--exclude=.scenery/", "--exclude=.env*", "--exclude=node_modules/", "--exclude=go.work", "--exclude=go.work.sum",
		"-e", "ssh -o BatchMode=yes -o ConnectTimeout=10", "--", "./", target+":"+begun.StagingPath)); err != nil {
		return abort(err)
	}
	if _, err := exchange("validate"); err != nil {
		return abort(err)
	}
	sourceRoot := begun.SourceRoot
	// down is idempotent for a stopped root; the agent socket location is
	// the target's own business, so it is not probed here.
	down := `if [ -f "` + sourceRoot + `/.scenery.json" ]; then scenery down --app-root "` + sourceRoot + `"; fi`
	if err := remote("remote scenery down", down); err != nil {
		return abort(err)
	}
	if _, err := exchange("activate"); err != nil {
		return err
	}
	up := `scenery up --detach --wait ready --env "` + envName + `" --app-root "` + sourceRoot + `"`
	recover := func(cause error) error {
		_ = remote("remote scenery down", down)
		restored, err := exchange("rollback")
		if err != nil {
			return errors.Join(cause, err)
		}
		if restored.Previous == "" {
			return fmt.Errorf("%w; the first release was not activated and the environment is stopped", cause)
		}
		if err := remote("remote scenery up", up); err != nil {
			return errors.Join(cause, fmt.Errorf("restore previous release %s: %w", restored.Previous, err))
		}
		return fmt.Errorf("%w; restored previous release %s with configuration revision %s", cause, restored.Previous, restored.ConfigRevision)
	}
	if err := remote("remote scenery up", up); err != nil {
		return recover(err)
	}
	if publishFrontends {
		// Production frontends are built and published on the remote host
		// after the dynamic runtime is ready: rsync deliberately excludes
		// ignored build output, and the remote publish step validates and
		// reloads the managed edge before reporting success.
		if err := remote("remote scenery deploy publish", `scenery deploy publish --env "`+envName+`" --app-root "`+sourceRoot+`" -o json`); err != nil {
			return recover(err)
		}
	}
	committed, err := exchange("commit")
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "deployment %s is active with configuration revision %s\n", deploymentID, committed.ConfigRevision)
	return nil
}

func parseDeploySSHOptions(target string, args []string) (deploySSHOptions, error) {
	var opts deploySSHOptions
	flags := newCLIFlagSet("deploy " + target)
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.StringVar(&opts.Env, "env", "", "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return deploySSHOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return deploySSHOptions{}, err
	}
	opts.Env = strings.TrimSpace(opts.Env)
	if cliFlagSet(flags, "env") && opts.Env == "" {
		return deploySSHOptions{}, usageErrorf("--env must not be empty")
	}
	return opts, nil
}
