package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	appcfg "scenery.sh/internal/app"
)

type deploySSHOptions struct {
	AppRoot string
	Env     string
}

func runDeploySSH(stdout io.Writer, target string, args []string) error {
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
			return err
		}
	}
	if err := runSceneryCheck(context.Background(), stdout, []string{"--app-root", appRoot}); err != nil {
		return fmt.Errorf("local scenery check: %w", err)
	}
	publishFrontends := strings.TrimSpace(env.Domain) != "" && len(productionFrontendNames(env)) > 0
	return runDeploySSHCommands(stdout, appRoot, cfg.AppID(), target, env.Name, publishFrontends)
}

func runDeploySSHCommands(stdout io.Writer, appRoot, appID, target, envName string, publishFrontends bool) error {
	remoteApp := "$HOME/.scenery/apps/" + appID
	steps := []struct {
		name string
		cmd  *exec.Cmd
	}{
		{
			name: "SSH preflight",
			cmd: exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "--", target,
				`command -v scenery >/dev/null && command -v rsync >/dev/null && mkdir -p "`+remoteApp+`"`),
		},
		{
			name: "remote scenery down",
			cmd: exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "--", target,
				`if [ -f "`+remoteApp+`/.scenery.json" ] && [ -S "$HOME/.scenery/run/agent.sock" ]; then scenery down --app-root "`+remoteApp+`"; fi`),
		},
		{
			name: "rsync",
			cmd: exec.Command("rsync", "-az", "--delete", "--filter=:- .gitignore", "--exclude=.git/", "--exclude=.scenery/", "--exclude=.env*", "--exclude=node_modules/", "--exclude=go.work", "--exclude=go.work.sum",
				"-e", "ssh -o BatchMode=yes -o ConnectTimeout=10", "--", "./", target+":.scenery/apps/"+appID+"/"),
		},
		{
			name: "remote scenery up",
			cmd: exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "--", target,
				`scenery up --detach --wait ready --env "`+envName+`" --app-root "`+remoteApp+`"`),
		},
	}
	if publishFrontends {
		// Production frontends are built and published on the remote host
		// after the dynamic runtime is ready: rsync deliberately excludes
		// ignored build output, and the remote publish step validates and
		// reloads the managed edge before reporting success.
		steps = append(steps, struct {
			name string
			cmd  *exec.Cmd
		}{
			name: "remote scenery deploy publish",
			cmd: exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "--", target,
				`scenery deploy publish --env "`+envName+`" --app-root "`+remoteApp+`" -o json`),
		})
	}
	for _, step := range steps {
		step.cmd.Dir = filepath.Clean(appRoot)
		step.cmd.Stdin = os.Stdin
		step.cmd.Stdout = stdout
		step.cmd.Stderr = cliStderr
		if err := step.cmd.Run(); err != nil {
			return fmt.Errorf("%s: %w", step.name, err)
		}
	}
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
		return deploySSHOptions{}, fmt.Errorf("--env must not be empty")
	}
	return opts, nil
}
