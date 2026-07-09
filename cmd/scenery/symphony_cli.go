package main

import (
	"context"
	"fmt"
	"os"

	"scenery.sh/internal/symphony"
)

type symphonyAutoOptions struct {
	AppRoot string
	On      bool
	Off     bool
}

func symphonyCommand(args []string) error {
	if len(args) == 0 || args[0] != "auto" {
		return fmt.Errorf("usage: scenery symphony auto --on|--off [--app-root <path>]")
	}
	return symphonyAutoCommand(args[1:])
}

func symphonyAutoCommand(args []string) error {
	opts, err := parseSymphonyAutoArgs(args)
	if err != nil {
		return err
	}
	if opts.On == opts.Off {
		return fmt.Errorf("usage: scenery symphony auto --on|--off [--app-root <path>]")
	}
	_, cfg, err := discoverConfiguredApp(opts.AppRoot)
	if err != nil {
		return err
	}
	ctx := context.Background()
	store, err := openDashboardSymphonyStore(ctx)
	if err != nil {
		return err
	}
	defer store.Close()
	appID := cfg.AppID()
	current, err := store.Workflow(ctx, appID)
	if err != nil {
		return err
	}
	mode := "manual"
	label := "disabled"
	if opts.On {
		mode = "auto"
		label = "enabled"
	}
	updated, err := store.UpdateWorkflow(ctx, appID, symphony.WorkflowInput{
		WorkflowMarkdown: current.WorkflowMarkdown,
		Mode:             mode,
		MaxConcurrency:   current.MaxConcurrency,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "symphony auto %s for %s\n", label, updated.AppID)
	return nil
}

func parseSymphonyAutoArgs(args []string) (symphonyAutoOptions, error) {
	var opts symphonyAutoOptions
	flags := newCLIFlagSet("symphony auto")
	flags.BoolVar(&opts.On, "on", false, "")
	flags.BoolVar(&opts.Off, "off", false, "")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return symphonyAutoOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return symphonyAutoOptions{}, err
	}
	return opts, nil
}
