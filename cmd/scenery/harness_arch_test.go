package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRunHarnessArchitectureStepValidAndInvalidFixtures(t *testing.T) {
	t.Parallel()

	t.Run("valid", func(t *testing.T) {
		t.Parallel()

		root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
		writeArchitectureSupportFiles(t, root)
		writeTestAppFile(t, root, "internal/example/example.go", "package example\n\nimport \"fmt\"\n\nfunc Format(v string) string { return fmt.Sprintf(\"%s\", v) }\n")
		writeTestAppFile(t, root, "apps/console/node_modules/pkg/README.md", "model context "+"protocol\n")

		step := runHarnessArchitectureStep(root)
		if !step.OK {
			t.Fatalf("architecture step failed: %+v", step)
		}
		if got, _ := step.Summary["source_files"].(int); got == 0 {
			t.Fatalf("source_files = %v, want > 0", step.Summary["source_files"])
		}
	})

	t.Run("invalid", func(t *testing.T) {
		t.Parallel()

		root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
		writeArchitectureSupportFiles(t, root)
		writeTestAppFile(t, root, "go.mod", "module scenery.sh\n\ngo 1.26.3\n\nrequire github.com/example/newdep v1.0.0\n")
		writeTestAppFile(t, root, "internal/bad/bad.go", "package bad\n\nimport _ \"github.com/spf13/cobra\"\n")
		writeTestAppFile(t, root, "internal/app/bad.go", "package app\n\nimport _ \"scenery.sh/internal/postgresdb\"\n")
		writeTestAppFile(t, root, "internal/model/bad.go", "package model\n\nimport _ \"golang.org/x/tools/go/packages\"\n")
		writeTestAppFile(t, root, "internal/scn/bad.go", "package scn\n\nimport _ \"scenery.sh/internal/compiler\"\n")
		writeTestAppFile(t, root, "internal/graph/bad.go", "package graph\n\nimport _ \"scenery.sh/internal/compiler\"\n")
		writeTestAppFile(t, root, "internal/compiler/bad.go", "package compiler\n\nimport _ \"scenery.sh/internal/evolution\"\n")
		writeTestAppFile(t, root, "runtime/bad.go", "package runtime\n\nimport _ \"scenery.sh/internal/devdash\"\n")
		writeTestAppFile(t, root, "ui/components/BadControl.tsx", "export function BadControl() { return <button>Bad</button>; }\n")

		step := runHarnessArchitectureStep(root)
		if step.OK {
			t.Fatalf("architecture step ok = true, want false")
		}
		joined := diagnosticMessages(step.Diagnostics)
		for _, want := range []string{
			"direct Go dependency is not in the architecture allowlist",
			"forbidden import github.com/spf13/cobra",
			"internal/app imports the PostgreSQL driver layer",
			"internal/model imports the parser package loader",
			"internal/scn stays foundational",
			"internal/graph stays below compiler and workflows",
			"internal/compiler stays below workflows",
			"UI catalog component contains raw interactive HTML",
		} {
			if !strings.Contains(joined, want) {
				t.Fatalf("missing %q diagnostic: %+v", want, step.Diagnostics)
			}
		}
	})
}

func TestCheckUICatalogAstryxComposition(t *testing.T) {
	t.Parallel()

	t.Run("rejects raw controls", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		path := filepath.Join(root, "Bad.tsx")
		writeTestAppFile(t, root, "Bad.tsx", "export const Bad = () => <input />;\n")
		diagnostics, err := checkUICatalogAstryxComposition(path, "ui/components/Bad.tsx")
		if err != nil {
			t.Fatal(err)
		}
		if len(diagnostics) != 1 || !strings.Contains(diagnostics[0].SuggestedAction, "Astryx primitive") {
			t.Fatalf("diagnostics = %+v, want one Astryx policy failure", diagnostics)
		}
	})

	t.Run("allows the documented filter pills exception", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		path := filepath.Join(root, "FilterPills.tsx")
		writeTestAppFile(t, root, "FilterPills.tsx", "export const FilterPills = () => <button>All</button>;\n")
		diagnostics, err := checkUICatalogAstryxComposition(path, "ui/components/FilterPills.tsx")
		if err != nil {
			t.Fatal(err)
		}
		if len(diagnostics) != 0 {
			t.Fatalf("diagnostics = %+v, want documented exception", diagnostics)
		}
	})
}

func TestCheckCurrentSurfaceResidue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		rel     string
		content string
		want    string
	}{
		{name: "current source selectors", rel: "testdata/app/scenery.scn", content: "language " + "{\n  edition = \"2027\"\n  " + "require_" + "profiles = []\n}\npackage \"app\" { " + "scenery_" + "version = \"any\" }\n", want: "authored language selector"},
		{name: "retired cookie", rel: "auth/session.go", content: "const cookie = \"" + "onlv_" + "refresh\"\n", want: "retired auth cookie name"},
		{name: "release selection", rel: "docs/local-contract.md", content: "Run `scenery upgrade " + "--version v1.2.3`.\n", want: "historical release selection"},
		{name: "active next name in path", rel: "internal/" + "v" + "next/compiler.go", content: "package compiler\n", want: "active next-generation name"},
		{name: "historical knowledge path", rel: "docs/knowledge.json", content: `"path": "docs/plans/0103-` + "v" + `next-language-and-onlv-house-migration.md",` + "\n"},
		{name: "active knowledge name", rel: "docs/knowledge.json", content: `"title": "New ` + "v" + `Next feature",` + "\n", want: "active next-generation name"},
		{name: "versioned logical identity", rel: "internal/spec/catalog.go", content: "const kind = \"scenery.record" + "/v1\"\n", want: "versioned first-party identity"},
		{name: "retained ABI", rel: "runtime/contract_registry.go", content: "const abi = \"scenery.go-runtime/v1\"\n"},
		{name: "legacy state migration detector", rel: "internal/evolution/recovery.go", content: "const legacy = \"scenery.change-transaction" + "/v1\"\n"},
		{name: "legacy identity outside migration", rel: "docs/current.md", content: "scenery.change-transaction" + "/v1\n", want: "versioned first-party identity"},
		{name: "historical plan is excluded", rel: "docs/plans/0001-history.md", content: "language " + "{ } scenery.record" + "/v1 " + "onlv_" + "refresh\n"},
		{name: "external toolchain and provenance remain", rel: "docs/current.md", content: "Go 1.26.3, OpenAPI 3.1.0, PostgreSQL 18, producer version v0.4.0.\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			path := filepath.Join(root, filepath.FromSlash(test.rel))
			writeTestAppFile(t, root, test.rel, test.content)
			diagnostics, err := checkCurrentSurfaceResidue(path, test.rel)
			if err != nil {
				t.Fatal(err)
			}
			joined := diagnosticMessages(diagnostics)
			if test.want == "" {
				if len(diagnostics) != 0 {
					t.Fatalf("diagnostics = %+v, want none", diagnostics)
				}
				return
			}
			if !strings.Contains(joined, test.want) {
				t.Fatalf("missing %q diagnostic: %+v", test.want, diagnostics)
			}
		})
	}
}

func writeArchitectureSupportFiles(t *testing.T, root string) {
	t.Helper()
	writeTestAppFile(t, root, ".gitignore", "/oracle/\n/coverage/\n.scenery/\n.DS_Store\nnode_modules/\n")
	writeTestAppFile(t, root, ".gitattributes", "cmd/scenery/devdash_static/** -diff linguist-generated=true linguist-vendored=true\ncmd/scenery/dashboard_static/dist/** -diff linguist-generated=true\n")
}
