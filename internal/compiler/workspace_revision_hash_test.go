package compiler

import "testing"

// These digests were computed independently from the canonical big-endian
// length framing. Ambiguous concatenations, UTF-8 names and binary data must
// retain exactly the same identity when the hashing implementation changes.
func TestWorkspaceRevisionCanonicalFraming(t *testing.T) {
	for _, test := range []struct {
		name    string
		entries map[string][]byte
		want    string
	}{
		{"empty", nil, "sha256:d1727e1539910b1be9e99fd292d76acccc82614e6c45a30fe80cf80dfc70f524"},
		{"short-name", map[string][]byte{"a": []byte("bc")}, "sha256:e3eca935fdaf65055863491ecf61e482d2d4b5a001895106f9c4d52fcb9a122b"},
		{"long-name", map[string][]byte{"ab": []byte("c")}, "sha256:9a177206ace3cebca2509d99e67b02993630a0f4cdda378e4a34b071bcc64956"},
		{"utf8-and-binary", map[string][]byte{"é.go": {0, 1, 255}, "empty": nil}, "sha256:ad3fb8996e5e50fdaae7281cf43c7132451024872cb27e6bd8f2dba81d75b6e2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := workspaceRevisionForEntries(test.entries); got != test.want {
				t.Fatalf("workspace revision = %s, want %s", got, test.want)
			}
		})
	}
}
