package runtime

import (
	"net/http"
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
		{name: "case insensitive q parameter", header: "gzip;Q=1, identity;Q=0", supported: []string{"gzip"}, want: "gzip"},
		{name: "case insensitive q zero rejects all", header: "gzip;Q=0, identity;Q=0", supported: []string{"gzip"}, errorText: "no acceptable response content encoding"},
		{name: "nan quality rejected", header: "gzip;q=NaN", supported: []string{"gzip"}, errorText: "invalid Accept-Encoding quality"},
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

func TestContractNegotiationHeadersJoinMultipleHeaderLines(t *testing.T) {
	req := &http.Request{Header: http.Header{
		"Accept":          []string{"application/problem+json", "application/json;q=0.5"},
		"Accept-Encoding": []string{"gzip;q=1", "identity;q=0"},
	}}
	accept, acceptEncoding := contractNegotiationHeaders(req)
	if accept != "application/problem+json, application/json;q=0.5" {
		t.Fatalf("accept = %q", accept)
	}
	if acceptEncoding != "gzip;q=1, identity;q=0" {
		t.Fatalf("acceptEncoding = %q", acceptEncoding)
	}
}
