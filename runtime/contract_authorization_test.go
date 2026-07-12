package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"scenery.sh/errs"
	"scenery.sh/runtime/shared"
)

func TestPopulateContractContextJSONAddsOnlyRuntimeTrustedValues(t *testing.T) {
	restore := enterState(&requestState{request: shared.Request{}, auth: AuthInfo{UID: "local:501", Data: map[string]any{"tenant_id": "tenant-1"}}})
	defer restore()
	mappings := []ContractContextMapping{{Source: "principal.tenant_id", Target: "tenant_id"}}
	raw, err := PopulateContractContextJSON([]byte(`{"scene_id":"scene-42"}`), mappings)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["tenant_id"] != "tenant-1" || got["scene_id"] != "scene-42" {
		t.Fatalf("context input = %#v", got)
	}
	if _, err := PopulateContractContextJSON([]byte(`{"tenant_id":"forged"}`), mappings); err == nil {
		t.Fatal("caller-supplied trusted context was accepted")
	}
}

func TestContractAuthorizationEvaluatesTypedPrincipalContextAndInput(t *testing.T) {
	state := &requestState{auth: AuthInfo{UID: "user-42", Data: map[string]any{"tenant_id": "tenant-1", "roles": []string{"member"}}}, request: shared.Request{Method: "POST", Path: "/house/process"}}
	restore := enterState(state)
	defer restore()
	policy := &ContractHTTPPolicy{
		BindingAddress: "house/binding/process", AuthorizationStrategy: "deny_unless_allowed",
		AuthorizationRules: []ContractAuthorizationRule{{Name: "member", Expression: `principal.uid == "user-42" && context.tenant_id == input.tenant_id && contains(principal.roles, "member")`}},
	}
	if err := authorizeContractInvocation(policy, struct {
		TenantID string `json:"tenant_id"`
	}{TenantID: "tenant-1"}); err != nil {
		t.Fatal(err)
	}
	if err := authorizeContractInvocation(policy, struct {
		TenantID string `json:"tenant_id"`
	}{TenantID: "other"}); errs.Code(err) != errs.PermissionDenied {
		t.Fatalf("denial = %v", err)
	}
}

func TestContractServerRunsAuthorizationBeforeHandler(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	called := false
	if err := RegisterEndpointChecked(&Endpoint{
		Service: "house", Name: "Denied", Access: Public, Path: "/denied", Methods: []string{http.MethodPost},
		ContractPolicy: &ContractHTTPPolicy{AuthorizationStrategy: "deny_unless_allowed", AuthorizationRuleCount: 1, AuthorizationRules: []ContractAuthorizationRule{{Name: "never", Expression: "false"}}, TransportStatuses: map[string]int{"admission.forbidden": http.StatusForbidden}},
		DecodeContractRequest: func(*http.Request, map[string]string) (ContractDecodedRequest, error) {
			return ContractDecodedRequest{Payload: map[string]any{"value": true}}, nil
		},
		Invoke: func(context.Context, []any, any) (any, error) { called = true; return map[string]any{}, nil },
		EncodeContractOutcome: func(*http.Request, any) (ContractHTTPResponse, error) {
			return ContractHTTPResponse{Status: http.StatusOK}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	server, err := newServer("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	server.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/denied", nil))
	if recorder.Code != http.StatusForbidden || called {
		t.Fatalf("status=%d called=%t body=%s", recorder.Code, called, recorder.Body.String())
	}
}

func TestContractAuthorizationFailsClosed(t *testing.T) {
	restore := enterState(&requestState{request: shared.Request{}})
	defer restore()
	for _, policy := range []*ContractHTTPPolicy{
		{AuthorizationStrategy: "none"},
		{AuthorizationStrategy: "deny_unless_allowed"},
		{AuthorizationStrategy: "deny_unless_allowed", AuthorizationRules: []ContractAuthorizationRule{{Expression: `principal.missing == true`}}},
	} {
		if err := authorizeContractInvocation(policy, map[string]any{}); errs.Code(err) != errs.PermissionDenied {
			t.Errorf("policy %#v returned %v", policy, err)
		}
	}
	if err := authorizeContractInvocation(&ContractHTTPPolicy{AuthorizationStrategy: "public"}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestContractAuthorizationRequiresRuntimeMintedLocalDeveloper(t *testing.T) {
	policy := &ContractHTTPPolicy{AuthorizationStrategy: "local_developer"}
	for _, auth := range []AuthInfo{
		{},
		{UID: "local:501", Data: map[string]any{"local_developer": false}},
	} {
		restore := enterState(&requestState{request: shared.Request{}, auth: auth})
		err := authorizeContractInvocation(policy, nil)
		restore()
		if errs.Code(err) != errs.PermissionDenied {
			t.Fatalf("auth %#v returned %v", auth, err)
		}
	}
	restore := enterState(&requestState{request: shared.Request{}, auth: AuthInfo{UID: "local:501", Data: map[string]any{"local_developer": true}}})
	defer restore()
	if err := authorizeContractInvocation(policy, nil); err != nil {
		t.Fatal(err)
	}
}

func TestContractAuthorizationDenyRulesTakePrecedence(t *testing.T) {
	restore := enterState(&requestState{request: shared.Request{}, auth: AuthInfo{UID: "blocked"}})
	defer restore()
	policy := &ContractHTTPPolicy{AuthorizationStrategy: "deny_unless_allowed", AuthorizationRules: []ContractAuthorizationRule{
		{Name: "allow_all", Effect: "allow", Expression: "true"},
		{Name: "blocked", Effect: "deny", Expression: `principal.uid == "blocked"`},
	}}
	if err := authorizeContractInvocation(policy, map[string]any{}); errs.Code(err) != errs.PermissionDenied {
		t.Fatalf("deny rule did not take precedence: %v", err)
	}
}
