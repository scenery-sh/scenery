package main

import (
	"context"
	"io"
	"os"
	"strings"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/envpolicy"
)

type checkDiagnostic struct {
	Stage           string `json:"stage"`
	Severity        string `json:"severity"`
	File            string `json:"file,omitempty"`
	Line            int    `json:"line,omitempty"`
	Column          int    `json:"column,omitempty"`
	Message         string `json:"message"`
	SuggestedAction string `json:"suggested_action,omitempty"`
}

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
	env, err := appEnvWithDotEnv(envpolicy.Environ(), appRoot, resolved.DotEnvFiles()...)
	if err != nil {
		return nil, err
	}
	missing := missingGoogleOAuthEnv(cfg.Auth.GoogleOAuth, env)
	if len(missing) == 0 {
		return diagnostics, nil
	}
	diagnostics = append(diagnostics, checkDiagnostic{
		Stage:           "auth",
		Severity:        "warning",
		File:            cfg.SourcePath(appRoot),
		Message:         "Google OAuth is enabled but credentials are missing: " + strings.Join(missing, ", "),
		SuggestedAction: "Set the missing env vars in the app environment or disable auth.google_oauth.enabled.",
	})
	return diagnostics, nil
}

func deployConfigInfoDiagnostics(appRoot string, cfg appcfg.Config) []checkDiagnostic {
	for name, raw := range cfg.Envs {
		if raw.Deploy == nil || strings.TrimSpace(raw.Domain) == "" || strings.TrimSpace(raw.Deploy.Root) != "" {
			continue
		}
		frontends := len(cfg.Frontends)
		if frontends == 1 {
			continue
		}
		reason := "no frontends are configured"
		if frontends > 1 {
			reason = "multiple frontends are configured"
		}
		return []checkDiagnostic{{
			Stage:           "config",
			Severity:        "info",
			File:            cfg.SourcePath(appRoot),
			Message:         "envs." + name + ".domain is set but deploy.root is unset; public / will serve a minimal page because " + reason,
			SuggestedAction: "Set envs." + name + ".deploy.root to \"api\" or the frontend that should own / on the public domain.",
		}}
	}
	return nil
}

func missingGoogleOAuthEnv(cfg appcfg.AuthGoogleConfig, env []string) []string {
	var missing []string
	if !hasAnyEnvValue(env, "GOOGLE_OAUTH_CLIENT_ID") {
		missing = append(missing, "GOOGLE_OAUTH_CLIENT_ID")
	}
	if !hasAnyEnvValue(env, "GOOGLE_OAUTH_CLIENT_SECRET") {
		missing = append(missing, "GOOGLE_OAUTH_CLIENT_SECRET")
	}
	return missing
}

func hasAnyEnvValue(env []string, names ...string) bool {
	for _, name := range names {
		if value, _ := lookupEnvValue(env, name); value != "" {
			return true
		}
	}
	return false
}
