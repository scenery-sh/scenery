package main

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"unicode"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/identityhash"
)

const appDatabaseURLEnv = "DATABASE_URL"

func shortIdentityHash(value string) string {
	return identityhash.Short(value)
}

func verifySubstrateOwner(substrate localagent.Substrate) error {
	if substrate.Owner.PID <= 0 && substrate.OwnerPID <= 0 {
		return fmt.Errorf("substrate owner is missing")
	}
	owner := substrate.Owner
	if owner.PID <= 0 {
		owner.PID = substrate.OwnerPID
	}
	if err := localagent.VerifyOwner(owner); err != nil {
		return err
	}
	for name, pid := range substrate.PIDs {
		if pid <= 0 {
			continue
		}
		componentOwner := substrate.Owners[name]
		if componentOwner.PID <= 0 {
			componentOwner.PID = pid
		}
		if err := localagent.VerifyOwner(componentOwner); err != nil {
			return fmt.Errorf("substrate component %s owner invalid: %w", name, err)
		}
	}
	return nil
}

func currentAgentSessionForAppRootWithClient(ctx context.Context, client *localagent.Client, appRoot string) (*localagent.Session, error) {
	sessions, err := client.List(ctx, appRoot)
	if err != nil {
		return nil, err
	}
	if len(sessions) == 0 {
		return nil, fmt.Errorf("no scenery dev runtime found for app root %s", appRoot)
	}
	return &sessions[0], nil
}

func localagentLabel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	dash := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func normalizeManagedTCPUpstream(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err == nil && parsed.Host != "" {
			return parsed.Host
		}
	}
	return value
}

func lookupEnvValue(env []string, key string) (string, string) {
	values := envListMap(env)
	if value := strings.TrimSpace(values[key]); value != "" {
		return value, key
	}
	return "", ""
}
