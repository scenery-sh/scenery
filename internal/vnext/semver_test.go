package vnext

import "testing"

func TestSemanticVersionConstraintsAreExact(t *testing.T) {
	for _, test := range []struct {
		version, constraint string
		want                bool
	}{
		{"2.1.0", ">= 2.1.0, < 3.0.0", true},
		{"3.0.0", ">= 2.1.0, < 3.0.0", false},
		{"1.2.3-beta.2", ">= 1.2.3-beta.1, < 1.2.3", true},
		{"1.2.3", "1.2.3", true},
		{"1.2", ">= 1.0.0", false},
	} {
		if got := semanticVersionSatisfies(test.version, test.constraint); got != test.want {
			t.Errorf("satisfies(%q, %q) = %t, want %t", test.version, test.constraint, got, test.want)
		}
	}
}
