package runtime

import (
	"strings"
	"testing"
)

func TestContractResponseEncodingPreferences(t *testing.T) {
	for _, test := range []struct {
		name, header    string
		supported       []string
		want, errorText string
	}{
		{name: "absent header", supported: []string{"gzip"}, want: "identity"},
		{name: "binding order breaks tie", header: "identity, gzip", supported: []string{"gzip"}, want: "gzip"},
		{name: "identity has higher quality", header: "gzip;q=0.5", supported: []string{"gzip"}, want: "identity"},
		{name: "explicit exclusion overrides wildcard", header: "*;q=1, gzip;q=0", supported: []string{"gzip"}, want: "identity"},
		{name: "explicit identity overrides wildcard", header: "*;q=0, identity;q=0.1", supported: []string{"gzip"}, want: "identity"},
		{name: "duplicate coding uses last quality", header: "gzip;q=1, gzip;q=0", supported: []string{"gzip", "gzip"}, want: "identity"},
		{name: "normalized configured coding", header: "gzip, identity;q=0", supported: []string{" GZip "}, want: "gzip"},
		{name: "unconfigured coding cannot replace identity", header: "gzip, identity;q=0", errorText: "no acceptable response content encoding"},
		{name: "unknown coding parameters still validated", header: "br;other=x", supported: []string{"gzip"}, errorText: "invalid Accept-Encoding parameter"},
		{name: "invalid configuration before empty header", supported: []string{"br"}, errorText: "unsupported configured response encoding"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := negotiateContractEncoding(test.header, test.supported)
			if test.errorText != "" {
				if err == nil || !strings.Contains(err.Error(), test.errorText) {
					t.Fatalf("encoding=%q error=%v, want %q", got, err, test.errorText)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("encoding=%q error=%v, want %q", got, err, test.want)
			}
		})
	}
}
