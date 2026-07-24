package main

import (
	"testing"

	localagent "scenery.sh/internal/agent"
)

func TestPathRouteManifestForLeaseRegistersRootFrontendOnce(t *testing.T) {
	t.Parallel()

	manifest := pathRouteManifestForLease(localagent.PortLease{
		Port: 4747,
		URL:  "http://localhost:4747",
	}, "app.local.test", nil, "web")
	root := manifest.Routes["root"]
	if root.Name != "root" || root.Kind != "frontend" || root.Path != "/" || root.URL != "http://localhost:4747/" || root.Backend != "web" {
		t.Fatalf("root route = %+v", root)
	}
	if _, ok := manifest.Routes["web"]; ok {
		t.Fatalf("root frontend unexpectedly has named route: %+v", manifest.Routes)
	}
	session := &localagent.Session{RouteManifest: manifest}
	if got := publicAppURLForSession(session); got != "http://localhost:4747/" {
		t.Fatalf("public app URL = %q", got)
	}
}

func TestPathRouteManifestRootFrontendUsesBackendLabelSanitizer(t *testing.T) {
	t.Parallel()

	manifest := pathRouteManifestForLease(localagent.PortLease{URL: "http://localhost:4748"}, "", nil, "web@2")
	if got, want := manifest.Routes["root"].Backend, localagentLabel("web@2"); got != want {
		t.Fatalf("root backend = %q, want %q", got, want)
	}
}
