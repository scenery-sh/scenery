package main

import "testing"

func TestParseGenClientArgsAcceptsTargetInvocation(t *testing.T) {
	opts, err := parseGenClientArgs([]string{
		"demoapp-dev",
		"--lang=typescript",
		"--output=apps/onlava/src/onlava-client.ts",
		"--app-root",
		"/tmp/app",
	})
	if err != nil {
		t.Fatalf("parseGenClientArgs() error = %v", err)
	}
	if opts.Target != "demoapp-dev" {
		t.Fatalf("Target = %q", opts.Target)
	}
	if opts.Lang != "typescript" {
		t.Fatalf("Lang = %q", opts.Lang)
	}
	if opts.Output != "apps/onlava/src/onlava-client.ts" {
		t.Fatalf("Output = %q", opts.Output)
	}
	if opts.AppRoot != "/tmp/app" {
		t.Fatalf("AppRoot = %q", opts.AppRoot)
	}
}
