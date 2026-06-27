package main

import (
	"testing"

	localagent "scenery.sh/internal/agent"
)

func TestLocalPathRouterShouldNotStripFrontendPrefix(t *testing.T) {
	t.Parallel()

	if localPathRouterShouldStripPrefix(localagent.RouteRecord{Kind: "frontend"}) {
		t.Fatal("frontend routes must preserve their base path for Vite and Astro dev servers")
	}
	if !localPathRouterShouldStripPrefix(localagent.RouteRecord{Kind: "api"}) {
		t.Fatal("non-frontend routes should keep the existing strip-prefix behavior")
	}
}

func TestLocalPathRouterRewriteHTMLRootRefs(t *testing.T) {
	t.Parallel()

	body := []byte(`<script type="module" src="/@vite/client"></script><link href="/blog"><img src="/profile.jpg">`)
	got := string(localPathRouterRewriteHTMLRootRefs(body, "/blog"))
	want := `<script type="module" src="/blog/@vite/client"></script><link href="/blog"><img src="/blog/profile.jpg">`
	if got != want {
		t.Fatalf("rewrite = %q, want %q", got, want)
	}
}
