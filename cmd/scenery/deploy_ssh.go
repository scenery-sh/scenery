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
			return err
		}
	}
	if err := runSceneryCheck(context.Background(), stdout, []string{"--app-root", appRoot}); err != nil {
		return fmt.Errorf("local scenery check: %w", err)
	}
	publishFrontends := strings.TrimSpace(env.Domain) != "" && len(productionFrontendNames(env)) > 0
	return runDeploySSHCommands(stdout, appRoot, cfg.AppID(), target, env.Name, publishFrontends, tools)
}

// deploySSHTools names the external programs a deploy shells out to and any
// extra environment they need. The zero value resolves "ssh" and "rsync" from
// PATH, which is what the CLI uses; tests set explicit paths so they can inject
// fakes without mutating the process PATH.
type deploySSHTools struct {
	SSH   string
	Rsync string
	Env   []string
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

func runDeploySSHCommands(stdout io.Writer, appRoot, appID, target, envName string, publishFrontends bool, tools deploySSHTools) error {
	remoteApp := "$HOME/.scenery/apps/" + appID
	steps := []struct {
		name string
		cmd  *exec.Cmd
	}{
		{
			name: "SSH preflight",
			cmd: exec.Command(tools.ssh(), "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "--", target,
				`command -v scenery >/dev/null && command -v rsync >/dev/null && mkdir -p "`+remoteApp+`"`),
		},
		{
			name: "remote scenery down",
			cmd: exec.Command(tools.ssh(), "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "--", target,
				`if [ -f "`+remoteApp+`/.scenery.json" ] && [ -S "$HOME/.scenery/run/agent.sock" ]; then scenery down --app-root "`+remoteApp+`"; fi`),
		},
		{
			name: "rsync",
			cmd: exec.Command(tools.rsync(), "-az", "--delete", "--filter=:- .gitignore", "--exclude=.git/", "--exclude=.scenery/", "--exclude=.env*", "--exclude=node_modules/", "--exclude=go.work", "--exclude=go.work.sum",
				"-e", "ssh -o BatchMode=yes -o ConnectTimeout=10", "--", "./", target+":.scenery/apps/"+appID+"/"),
		},
		{
			name: "remote scenery up",
			cmd: exec.Command(tools.ssh(), "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "--", target,
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
			cmd: exec.Command(tools.ssh(), "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "--", target,
				`scenery deploy publish --env "`+envName+`" --app-root "`+remoteApp+`" -o json`),
		})
	}
	for _, step := range steps {
		step.cmd.Dir = filepath.Clean(appRoot)
		if len(tools.Env) > 0 {
			// Extend the command's own environment rather than the process
			// environment: with a nil Env the exec package derives PWD from
			// Dir, and rsync's logged working directory depends on it. Copying
			// the raw process environment would silently switch the child to
			// getcwd()'s symlink-resolved path.
			step.cmd.Env = append(step.cmd.Environ(), tools.Env...)
		}
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
