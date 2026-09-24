package main

import (
	"context"
	"io"
	"os"
	"strings"

	appcfg "scenery.sh/internal/app"
)

func checkCommand(args []string) error {
	return runSceneryCheck(context.Background(), os.Stdout, args)
}

func runSceneryCheck(_ context.Context, stdout io.Writer, args []string) error {
	return runContractCheck(stdout, args)
}

func checkWarningDiagnostics(appRoot string, cfg appcfg.Config) ([]checkDiagnostic, error) {
	diagnostics := deployConfigInfoDiagnostics(appRoot, cfg)
	if !cfg.Auth.Enabled || !cfg.Auth.GoogleOAuth.Enabled {
		return diagnostics, nil
	}
	resolved, err := cfg.ResolveEnv("")
	if err != nil {
		return nil, err
	}
	missing, err := missingGoogleOAuthConfig(cfg, resolved)
	if err != nil {
		return nil, err
	}
	if len(missing) == 0 {
		return diagnostics, nil
	}
	diagnostics = append(diagnostics, checkDiagnostic{
		Stage:           "auth",
		Severity:        "warning",
		File:            cfg.SourcePath(appRoot),
		Message:         "Google OAuth is enabled but the " + resolved.Name + " environment does not configure: " + strings.Join(missing, ", "),
		SuggestedAction: "Configure them with scenery config set <key> --env " + resolved.Name + " or disable auth.google_oauth.enabled.",
	})
	return diagnostics, nil
}

func deployConfigInfoDiagnostics(appRoot string, cfg appcfg.Config) []checkDiagnostic {
	for name, raw := range cfg.Envs {
		if raw.Deploy == nil || strings.TrimSpace(raw.Domain) == "" || cfg.RootFrontend() != "" {
			continue
		}
		frontends := len(cfg.Frontends)
		reason := "no frontends are configured"
		if frontends > 1 {
			reason = "multiple frontends are configured"
		}
		return []checkDiagnostic{{
			Stage:           "config",
			Severity:        "info",
			File:            cfg.SourcePath(appRoot),
			Message:         "envs." + name + ".domain is set but top-level root is unset; public / will serve a minimal page because " + reason,
			SuggestedAction: "Set top-level \"root\" to the frontend that should own / across local development and deployment.",
		}}
	}
	return nil
}

// missingGoogleOAuthConfig reads which Google OAuth inputs the default local
// environment leaves unconfigured. A deployable default environment lives on
// its target and is checked by deployment readiness instead.
func missingGoogleOAuthConfig(cfg appcfg.Config, env appcfg.ResolvedEnv) ([]string, error) {
	if env.Deployable() {
		return nil, nil
	}
	store, err := devConfigStore(cfg)
	if err != nil {
		return nil, err
	}
	document, err := devConfigDocument(store, cfg, env)
	if err != nil {
		return nil, err
	}
	var missing []string
	if _, ok := document.Values["auth.google_client_id"]; !ok {
		missing = append(missing, "auth.google_client_id")
	}
	if _, ok := document.Secrets["auth.google_client_secret"]; !ok {
		missing = append(missing, "auth.google_client_secret")
	}
	return missing, nil
}
