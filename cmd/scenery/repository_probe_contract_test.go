package main

import "testing"

// Parser-only assertions formerly embedded in repository drift orchestration
// stay with the product parser; the verifier executes the public read commands.
func TestRepositoryProbeParserContract(t *testing.T) {
	if _, err := parseInspectArgs([]string{"ui", "--frontend", "web", "--app-root", "/fixture", "-o", "json"}); err != nil {
		t.Fatal(err)
	}
	if _, err := parseStatusArgs([]string{"-o", "json", "--app-root", "/fixture", "--watch"}); err != nil {
		t.Fatal(err)
	}
}
