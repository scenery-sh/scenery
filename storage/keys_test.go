package storage

import "testing"

func TestValidateKey(t *testing.T) {
	t.Parallel()
	valid := []string{"a.txt", "dir/file.json", "tenant_1/jobs-2.map"}
	for _, key := range valid {
		if err := ValidateKey(key); err != nil {
			t.Fatalf("ValidateKey(%q) returned error: %v", key, err)
		}
	}
	invalid := []string{"", "/abs", "a//b", "a/./b", "a/../b", `a\b`, "a\nb", "dir/"}
	for _, key := range invalid {
		if err := ValidateKey(key); err == nil {
			t.Fatalf("ValidateKey(%q) returned nil, want error", key)
		}
	}
}

func TestNormalizeListOptions(t *testing.T) {
	t.Parallel()
	opts, err := NormalizeListOptions(ListOptions{Prefix: "dir/", Delimiter: "/", Limit: 0})
	if err != nil {
		t.Fatalf("NormalizeListOptions returned error: %v", err)
	}
	if opts.Limit != DefaultListLimit {
		t.Fatalf("default limit = %d, want %d", opts.Limit, DefaultListLimit)
	}
	opts, err = NormalizeListOptions(ListOptions{Limit: MaxListLimit + 10})
	if err != nil {
		t.Fatalf("NormalizeListOptions clamp returned error: %v", err)
	}
	if opts.Limit != MaxListLimit {
		t.Fatalf("clamped limit = %d, want %d", opts.Limit, MaxListLimit)
	}
	if _, err := NormalizeListOptions(ListOptions{Delimiter: ":"}); err == nil {
		t.Fatal("NormalizeListOptions accepted unsupported delimiter")
	}
}
