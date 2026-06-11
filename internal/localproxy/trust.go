package localproxy

import "strings"

var installLocalCATrust = installLocalCATrustOS
var localCATrusted = localCATrustedOS

func certificateOutputHasSHA256(out []byte, fingerprint string) bool {
	want := normalizeCertificateFingerprint(fingerprint)
	if want == "" {
		return false
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "SHA-256 hash:") {
			continue
		}
		got := normalizeCertificateFingerprint(strings.TrimPrefix(line, "SHA-256 hash:"))
		if got == want {
			return true
		}
	}
	return false
}

func normalizeCertificateFingerprint(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, ":", "")
	value = strings.ReplaceAll(value, " ", "")
	return strings.ToUpper(value)
}
