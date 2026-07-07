package inspect

import (
	"testing"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/model"
)

func TestStandardAuthGoogleEndpointsFollowConfig(t *testing.T) {
	t.Parallel()

	cfg := appcfg.Config{Auth: appcfg.AuthConfig{Enabled: true}}
	resp := BuildEndpointsResponse("/tmp/app", cfg, &model.App{})
	if endpointRecord(resp.Endpoints, "GoogleStart") != nil || endpointRecord(resp.Endpoints, "GoogleCallback") != nil {
		t.Fatal("disabled Google OAuth endpoints leaked into inspect endpoints")
	}

	cfg.Auth.GoogleOAuth.Enabled = true
	resp = BuildEndpointsResponse("/tmp/app", cfg, &model.App{})
	if endpointRecord(resp.Endpoints, "GoogleStart") == nil || endpointRecord(resp.Endpoints, "GoogleCallback") == nil {
		t.Fatal("enabled Google OAuth endpoints missing from inspect endpoints")
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
