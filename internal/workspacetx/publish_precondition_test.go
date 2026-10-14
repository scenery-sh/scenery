package workspacetx

import "testing"

func TestPublicationPreconditionBindsBytesAndPresence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		expected string
		exists   bool
		before   []byte
		valid    bool
	}{
		{"unchanged", digest([]byte("verified")), true, []byte("verified"), true},
		{"edited", digest([]byte("verified")), true, []byte("editor"), false},
		{"removed", digest([]byte("verified")), false, nil, false},
		{"absent", AbsentDigest, false, nil, true},
		{"appeared", AbsentDigest, true, []byte("editor"), false},
		{"empty appeared", AbsentDigest, true, []byte{}, false},
		{"empty file", digest(nil), true, []byte{}, true},
		{"empty removed", digest(nil), false, nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := verifyExpectedBefore(File{Path: "output.go", ExpectedBeforeDigest: test.expected}, test.exists, test.before)
			if (err == nil) != test.valid {
				t.Fatalf("valid = %v, want %v: %v", err == nil, test.valid, err)
			}
		})
	}
}
