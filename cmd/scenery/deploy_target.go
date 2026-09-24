package main

import (
	"slices"
	"strings"

	appcfg "scenery.sh/internal/app"
)

// deployCommandNames are the words `scenery deploy` reads as commands rather
// than as an SSH target.
var deployCommandNames = []string{"setup", "status", "enable", "disable", "publish", "resume", "teardown", "plan", "apply"}

// deployTargetTypoError refuses a deploy operand that is a misspelled deploy
// command, such as `statsu`, instead of treating it as an SSH target. Only an
// SSH target the app lists in `envs.<name>.deploy.ssh` can be deployed to, so
// a word close to a command is a target only when the app lists it.
func deployTargetTypoError(target string, configured []string) error {
	suggestions := closestNames(target, deployCommandNames, false)
	if len(suggestions) == 0 || slices.Contains(configured, target) {
		return nil
	}
	return usageErrorf("unknown deploy command %q; did you mean %q? Run `scenery help deploy`", target, suggestions[0])
}

// configuredDeployTargets lists the SSH targets of every environment of the
// app that args select (`--app-root`, or the current directory); an app that
// cannot be discovered lists none.
func configuredDeployTargets(args []string) []string {
	start := ""
	for index, argument := range args {
		if argument == "--app-root" && index+1 < len(args) {
			start = args[index+1]
		} else if value, ok := strings.CutPrefix(argument, "--app-root="); ok {
			start = value
		}
	}
	start, err := resolveAppRoot(start)
	if err != nil {
		return nil
	}
	_, cfg, err := appcfg.DiscoverRoot(start)
	if err != nil {
		return nil
	}
	var targets []string
	for _, env := range cfg.Envs {
		if env.Deploy != nil {
			targets = append(targets, env.Deploy.SSH...)
		}
	}
	return targets
}
