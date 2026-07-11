package vnext

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestConcreteSyntaxTreeIsLosslessAndRecordsCRLFTriviaComments(t *testing.T) {
	root := t.TempDir()
	sourceBytes := []byte("# leading\r\nrecord \"item\" {  # trailing\r\n\r\n  # detached\r\n\r\n  # field\r\n  field \"name\" {\r\n    type = string\r\n  }\r\n}\r\n")
	path := filepath.Join(root, "scenery.scn")
	if err := os.WriteFile(path, sourceBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	source, diagnostics := parseSource(root, path)
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
			path := filepath.Join(root, "scenery.scn")
			if err := os.WriteFile(path, test.data, 0o644); err != nil {
				t.Fatal(err)
			}
			_, diagnostics := parseSource(root, path)
			if !hasDiagnostic(diagnostics, test.code) {
				t.Fatalf("diagnostics = %#v", diagnostics)
			}
		})
	}
}

func TestParserRejectsNonCanonicalIdentifiers(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "scenery.scn")
	if err := os.WriteFile(path, []byte("record \"Not_Snake\" {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, diagnostics := parseSource(root, path)
	if !hasDiagnostic(diagnostics, "SCN1013") {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}
