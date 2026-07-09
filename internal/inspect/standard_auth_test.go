package inspect

import (
	"testing"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/model"
)

func TestStandardAuthGoogleEndpointsFollowConfig(t *testing.T) {
	t.Parallel()

	googleEndpoints := []string{
		"DisconnectGoogleConnection",
		"GetGoogleConnection",
		"GoogleCallback",
		"GoogleConnectCallback",
		"GoogleConnectStart",
		"GoogleStart",
	}
	cfg := appcfg.Config{Auth: appcfg.AuthConfig{Enabled: true}}
	resp := BuildEndpointsResponse("/tmp/app", cfg, &model.App{})
	for _, name := range googleEndpoints {
		if endpointRecord(resp.Endpoints, name) != nil {
			t.Fatalf("disabled Google OAuth endpoint %s leaked into inspect endpoints", name)
		}
	}

	cfg.Auth.GoogleOAuth.Enabled = true
	resp = BuildEndpointsResponse("/tmp/app", cfg, &model.App{})
	for _, name := range googleEndpoints {
		if endpointRecord(resp.Endpoints, name) == nil {
			t.Fatalf("enabled Google OAuth endpoint %s missing from inspect endpoints", name)
		}
	}
}

func endpointRecord(endpoints []EndpointRecord, name string) *EndpointRecord {
	for i := range endpoints {
		if endpoints[i].Endpoint == name {
			return &endpoints[i]
		}
	}
	return nil
}
