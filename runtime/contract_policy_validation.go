package runtime

import "fmt"

func validateContractHTTPPolicy(policy *ContractHTTPPolicy) error {
	if policy == nil {
		return nil
	}
	if policy.FrameworkGuarantee != "" && policy.FrameworkGuarantee != "framework_enforced" {
		return fmt.Errorf("HTTP codec guarantee must be framework_enforced")
	}
	if policy.AuthorizationRuleCount != len(policy.AuthorizationRules) {
		return fmt.Errorf("authorization rule count mismatch: declared %d, received %d", policy.AuthorizationRuleCount, len(policy.AuthorizationRules))
	}
	strategies := map[string]bool{"public": true, "none": true, "local_developer": true, "deny_unless_allowed": true, "any_allow": true, "allow_if_any": true, "all_must_allow": true, "allow_if_all": true, "first_applicable": true}
	if !strategies[policy.AuthorizationStrategy] {
		return fmt.Errorf("unsupported authorization strategy %q", policy.AuthorizationStrategy)
	}
	knownSteps := map[string]bool{"std.middleware.request_id": true, "std.middleware.trace": true, "std.middleware.recover": true}
	seen := map[string]bool{}
	for _, step := range policy.PipelineSteps {
		if !knownSteps[step] {
			return fmt.Errorf("unsupported pipeline step %q", step)
		}
		if seen[step] {
			return fmt.Errorf("duplicate exclusive pipeline step %q", step)
		}
		seen[step] = true
	}
	for _, rule := range policy.AuthorizationRules {
		if rule.Name == "" {
			return fmt.Errorf("authorization rule name is required")
		}
		if err := ValidateContractAuthorizationExpression(rule.Expression); err != nil {
			return fmt.Errorf("authorization rule %s: %w", rule.Name, err)
		}
	}
	return nil
}
