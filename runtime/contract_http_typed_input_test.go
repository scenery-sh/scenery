package runtime

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestContractInputTypedTargetPreservesPartialDecode(t *testing.T) {
	request := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"decoded","unknown":true}`))
		r.Header.Set("Content-Type", "application/json")
		return r
	}
	value, err := DecodeContractJSON[mappedContractInput](request())
	if err == nil || value.Name != "decoded" {
		t.Fatalf("strict partial decode = %#v, %v", value, err)
	}
	pointer, err := DecodeContractJSON[*mappedContractInput](request())
	if err == nil || pointer == nil || pointer.Name != "decoded" {
		t.Fatalf("pointer partial decode = %#v, %v", pointer, err)
	}
	cause := errors.New("custom decode failed after populating target")
	schema := ContractRequestSchema{Body: &ContractBodyMapping{
		Codec: "json",
		DecodeValue: func(_ []byte, target any) error {
			typed, ok := target.(**mappedContractInput)
			if !ok {
				t.Fatalf("custom decoder target = %T, want **mappedContractInput", target)
			}
			*typed = &mappedContractInput{Name: "custom"}
			return cause
		},
	}}
	pointer, err = DecodeContractInput[*mappedContractInput](request(), nil, schema)
	if !errors.Is(err, cause) || pointer == nil || pointer.Name != "custom" {
		t.Fatalf("custom partial decode = %#v, %v", pointer, err)
	}
}
