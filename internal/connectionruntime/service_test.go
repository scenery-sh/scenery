package connectionruntime

import (
	"encoding/base64"
	"testing"

	"scenery.sh/internal/appsdk"
	"scenery.sh/internal/connectionapi"
	"scenery.sh/internal/connectionvault"
	"scenery.sh/runtime/shared"
)

type configurationTestHost struct {
	appsdk.Host
	callback string
}

func (h configurationTestHost) FrameworkConfigString(key string) (string, bool, error) {
	if key == "auth.connections.google.callback_uri" {
		return h.callback, h.callback != "", nil
	}
	return "client", true, nil
}
func (configurationTestHost) FrameworkConfigSecret(key string) ([]byte, bool, error) {
	if key == "auth.token_cipher_key" {
		return nil, false, nil
	}
	return []byte(" vector-jwt "), true, nil
}

func TestLegacyLocalCipherAndDefaultCallbackRemainCompatible(t *testing.T) {
	previous := *appsdk.Metadata()
	t.Cleanup(func() { appsdk.SetMetadata(previous) })
	appsdk.SetMetadata(shared.AppMetadata{})
	service, err := New(Options{Host: configurationTestHost{}, Providers: []connectionapi.ProviderConfig{{ID: "google", Preset: "google"}}})
	if err != nil {
		t.Fatal(err)
	}
	appsdk.SetMetadata(shared.AppMetadata{APIBaseURL: "https://app.example.test/api", Environment: shared.Environment{Cloud: shared.CloudLocal}})
	provider, err := service.provider("google")
	if err != nil {
		t.Fatal(err)
	}
	if provider.callbackURI != "https://app.example.test/api/auth/connections/google/callback" {
		t.Fatal("default callback lost the advertised API prefix")
	}
	// Frozen version-1 envelope under the original JWT-derived key, with a
	// deterministic test nonce. It must remain readable without re-encryption.
	envelope, err := base64.StdEncoding.DecodeString("AQABAgMEBQYHCAkKC8NztflpDF2Ltq+nsveI4wr12O6291FoKqXpt5IVyAgn3M2g")
	if err != nil {
		t.Fatal(err)
	}
	cipher := connectionvault.TokenCipher{Key: service.cipherKey}
	if token, err := cipher.Open(envelope); err != nil || token != "legacy-vector-token" {
		t.Fatalf("legacy envelope/key compatibility: %v", err)
	}
	envelope[len(envelope)-1] ^= 1
	if _, err := cipher.Open(envelope); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	for _, callback := range []string{"ftp://app.example.test/auth/connections/google/callback", "http://app.example.test/auth/connections/google/callback", "https://app.example.test/wrong"} {
		if _, err := New(Options{Host: configurationTestHost{callback: callback}, Providers: []connectionapi.ProviderConfig{{ID: "google", Preset: "google"}}}); err == nil {
			t.Fatalf("unsafe callback accepted: %s", callback)
		}
	}
}
