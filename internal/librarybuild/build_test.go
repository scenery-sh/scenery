package librarybuild

import (
	"runtime"
	"slices"
	"testing"
)

func TestResolvePlatformsUsesExactSupportedMatrix(t *testing.T) {
	platforms, err := resolvePlatforms(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(platforms, []string{"darwin/arm64", "linux/amd64"}) {
		t.Fatalf("platforms = %#v", platforms)
	}
	host, err := resolvePlatforms([]string{"host"})
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" || runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		if err != nil || len(host) != 1 {
			t.Fatalf("host = %#v, %v", host, err)
		}
	} else if err == nil {
		t.Fatalf("unsupported host accepted: %#v", host)
	}
	if _, err := resolvePlatforms([]string{"windows/amd64"}); err == nil {
		t.Fatal("unsupported platform accepted")
	}
}
