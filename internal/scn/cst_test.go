package scn

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConcreteSyntaxTreeIsLosslessAndRecordsCRLFTriviaComments(t *testing.T) {
	root := t.TempDir()
	sourceBytes := []byte("# leading\r\nrecord \"item\" {  # trailing\r\n\r\n  # detached\r\n\r\n  # field\r\n  field \"name\" {\r\n    type = string\r\n  }\r\n}\r\n")
	path := filepath.Join(root, AppFilename)
	if err := os.WriteFile(path, sourceBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	source, diagnostics := Parse(root, path)
	if hasErrors(diagnostics) {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if source.CST == nil || !bytes.Equal(source.CST.Bytes(), sourceBytes) || source.CST.LineEndings != "crlf" {
		t.Fatalf("CST = %#v bytes=%q", source.CST, source.CST.Bytes())
	}
	attachments := map[string]bool{}
	for _, comment := range source.CST.Comments {
		attachments[comment.Attachment] = true
	}
	for _, attachment := range []string{"leading", "trailing", "detached"} {
		if !attachments[attachment] {
			t.Errorf("missing %s comment in %#v", attachment, source.CST.Comments)
		}
	}
}

func TestParserRejectsBOMInvalidUTF8AndNonCanonicalComments(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		code string
	}{
		{name: "bom", data: append([]byte{0xef, 0xbb, 0xbf}, []byte("record \"x\" {}\n")...), code: "SCN1011"},
		{name: "utf8", data: []byte{'r', 'e', 'c', 'o', 'r', 'd', ' ', '"', 0xff, '"', ' ', '{', '}'}, code: "SCN1011"},
		{name: "slash comment", data: []byte("// not canonical\nrecord \"x\" {}\n"), code: "SCN1012"},
		{name: "block comment", data: []byte("/* not canonical */\nrecord \"x\" {}\n"), code: "SCN1012"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, AppFilename)
			if err := os.WriteFile(path, test.data, 0o644); err != nil {
				t.Fatal(err)
			}
			_, diagnostics := Parse(root, path)
			if !hasDiagnostic(diagnostics, test.code) {
				t.Fatalf("diagnostics = %#v", diagnostics)
			}
		})
	}
}

func TestParserRejectsNonCanonicalIdentifiers(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, AppFilename)
	if err := os.WriteFile(path, []byte("record \"Not_Snake\" {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, diagnostics := Parse(root, path)
	if !hasDiagnostic(diagnostics, "SCN1013") {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestConcreteSyntaxTreeRoundTripsAssistantMCPBlockShapes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, AppFilename)
	sourceBytes := []byte(`# binding
binding "process_scene_mcp" {
  mcp {
    name = "process_scene"
  }
}

mcp_connection "docs" {
  auth {
    scheme = "bearer"
  }
  tools {
    allow = ["search", "fetch"]
  }
}

mcp_server "support" {
  capability "process_scene" {
    binding = module.house.process_scene_mcp
  }
  connection "docs" {
    connection = mcp_connection.docs
  }
}

assistant "support" {
  implementation {
    adapter = "eve"
  }
  surface {
    path = "/assistants/support"
  }
}
`)
	if err := os.WriteFile(path, sourceBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	source, diagnostics := Parse(root, path)
	if hasErrors(diagnostics) {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if source == nil || source.CST == nil || !bytes.Equal(source.CST.Bytes(), sourceBytes) {
		t.Fatalf("CST did not preserve source bytes: %#v", source.CST)
	}

	var signatures []string
	var visit func(string, []*Block)
	visit = func(parent string, blocks []*Block) {
		for _, block := range blocks {
			address := block.Type
			if len(block.Labels) > 0 {
				address += "[" + strings.Join(block.Labels, ",") + "]"
			}
			if parent != "" {
				address = parent + "." + address
			}
			signatures = append(signatures, address)
			visit(address, block.Blocks)
		}
	}
	visit("", source.Blocks)
	want := []string{
		"binding[process_scene_mcp]",
		"binding[process_scene_mcp].mcp",
		"mcp_connection[docs]",
		"mcp_connection[docs].auth",
		"mcp_connection[docs].tools",
		"mcp_server[support]",
		"mcp_server[support].capability[process_scene]",
		"mcp_server[support].connection[docs]",
		"assistant[support]",
		"assistant[support].implementation",
		"assistant[support].surface",
	}
	if strings.Join(signatures, "\n") != strings.Join(want, "\n") {
		t.Fatalf("block signatures = %#v, want %#v", signatures, want)
	}

	leadingTargets := map[string]bool{}
	for _, comment := range source.CST.Comments {
		if comment.Attachment == "leading" {
			leadingTargets[comment.Target] = true
		}
	}
	if !leadingTargets["block:binding:process_scene_mcp"] {
		t.Fatalf("leading comment targets = %#v", leadingTargets)
	}
}

func hasErrors(diagnostics []Diagnostic) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			return true
		}
	}
	return false
}

func hasDiagnostic(diagnostics []Diagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}
