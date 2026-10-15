package oidc

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type keyTransport func(*http.Request) (*http.Response, error)

func (f keyTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestJWKSRejectsNoncanonicalAndWeakRSAIntegers(t *testing.T) {
	strong := make([]byte, 256)
	strong[0], strong[255] = 0x80, 1
	shortBits := append([]byte(nil), strong...)
	shortBits[0] = 0x40 // 256 bytes, but only 2047 bits
	weakPadded := make([]byte, 256)
	weakPadded[192], weakPadded[255] = 0x80, 1
	encode := base64.RawURLEncoding.EncodeToString
	for _, scenario := range []struct {
		name, n, e string
		valid      bool
	}{
		{"canonical 2048", encode(strong), "AQAB", true},
		{"2047 bits", encode(shortBits), "AQAB", false},
		{"padded 512 bits", encode(weakPadded), "AQAB", false},
		{"leading modulus zero", encode(append([]byte{0}, strong...)), "AQAB", false},
		{"leading exponent zero", encode(strong), "AAEAAQ", false},
		{"even exponent", encode(strong), "AQAC", false},
		{"noncanonical base64", encode(strong) + "\n", "AQAB", false},
		{"nonzero padding bits", encode(strong)[:len(encode(strong))-1] + "R", "AQAB", false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{"keys": []any{map[string]string{"kid": "key", "kty": "RSA", "n": scenario.n, "e": scenario.e}}})
			if err != nil {
				t.Fatal(err)
			}
			v := &Validator{JWKSURL: "https://issuer.test/keys", Client: &http.Client{Transport: keyTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			})}}
			key, err := v.key(t.Context(), "key", time.Unix(1, 0))
			if (err == nil) != scenario.valid || scenario.valid && key.N.BitLen() != 2048 {
				t.Fatalf("JWKS admission: key=%v error=%v", key, err)
			}
		})
	}
}
