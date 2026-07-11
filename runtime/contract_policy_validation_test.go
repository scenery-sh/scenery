package runtime

import "testing"

func TestContractPolicyRegistrationRejectsMetadataMismatch(t *testing.T) {
	for _, policy := range []*ContractHTTPPolicy{
		{AuthorizationStrategy: "deny_unless_allowed", AuthorizationRuleCount: 1},
		{AuthorizationStrategy: "unknown"},
		{AuthorizationStrategy: "public", PipelineSteps: []string{"std.middleware.trace", "std.middleware.trace"}},
		{AuthorizationStrategy: "public", PipelineSteps: []string{"custom"}},
		{AuthorizationStrategy: "public", FrameworkGuarantee: "implementation_declared"},
	} {
		if err := validateContractHTTPPolicy(policy); err == nil {
			t.Errorf("policy %#v was accepted", policy)
		}
	}
}
