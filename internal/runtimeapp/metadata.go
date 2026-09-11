package runtimeapp

import (
	"strings"

	"scenery.sh/internal/envpolicy"
	"scenery.sh/runtime/shared"
)

// ResolveMetadata uses the same runtime environment in a host or native worker.
// Listener addresses are runtime inputs, never linked artifact identities.
func ResolveMetadata(name, listenAddr string) shared.AppMetadata {
	baseID := strings.TrimSpace(envpolicy.Get("SCENERY_BASE_APP_ID"))
	if baseID == "" {
		baseID = name
	}
	runtimeID := strings.TrimSpace(envpolicy.Get("SCENERY_RUNTIME_APP_ID"))
	if runtimeID == "" {
		runtimeID = baseID
	}
	baseURL := strings.TrimSpace(envpolicy.Get("SCENERY_PUBLIC_BASE_URL"))
	if baseURL == "" {
		baseURL = "http://" + listenAddr
	}
	return shared.AppMetadata{AppID: name, BaseAppID: baseID, RuntimeAppID: runtimeID, SessionID: strings.TrimSpace(envpolicy.Get("SCENERY_SESSION_ID")), Environment: DefaultEnvironment(), APIBaseURL: baseURL}
}
