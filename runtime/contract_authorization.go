package runtime

import (
	"bytes"
	"encoding/json"

	"scenery.sh/errs"
)

func authorizeContractInvocation(policy *ContractHTTPPolicy, input any) error {
	if policy == nil || policy.AuthorizationStrategy == "public" {
		return nil
	}
	if policy.AuthorizationStrategy == "local_developer" {
		auth := CurrentAuth()
		if auth != nil && auth.UID != "" {
			data, _ := auth.Data.(map[string]any)
			if local, _ := data["local_developer"].(bool); local {
				return nil
			}
		}
		return contractAuthorizationDenied("local developer authorization is required")
	}
	if policy.AuthorizationStrategy == "none" || len(policy.AuthorizationRules) == 0 {
		return contractAuthorizationDenied("authorization policy did not allow invocation")
	}
	variables, err := contractAuthorizationVariables(policy, input)
	if err != nil {
		return contractAuthorizationDenied("authorization context is unavailable")
	}
	allowed := make([]bool, 0, len(policy.AuthorizationRules))
	for _, rule := range policy.AuthorizationRules {
		value, err := evaluateContractAuthorizationRule(rule.Expression, variables)
		if err != nil {
			return contractAuthorizationDenied("authorization rule evaluation failed")
		}
		if rule.Effect == "deny" {
			if value {
				return contractAuthorizationDenied("authorization deny rule matched")
			}
			continue
		}
		allowed = append(allowed, value)
	}
	if len(allowed) == 0 {
		return contractAuthorizationDenied("authorization policy did not allow invocation")
	}
	switch policy.AuthorizationStrategy {
	case "deny_unless_allowed", "any_allow", "allow_if_any":
		for _, value := range allowed {
			if value {
				return nil
			}
		}
	case "all_must_allow", "allow_if_all":
		for _, value := range allowed {
			if !value {
				return contractAuthorizationDenied("authorization policy denied invocation")
			}
		}
		return nil
	case "first_applicable":
		if allowed[0] {
			return nil
		}
	default:
		return contractAuthorizationDenied("unknown authorization strategy")
	}
	return contractAuthorizationDenied("authorization policy denied invocation")
}

func contractAuthorizationDenied(message string) error {
	return errs.B().Code(errs.PermissionDenied).Msg(message).Err()
}

func contractAuthorizationVariables(policy *ContractHTTPPolicy, input any) (map[string]any, error) {
	principal := map[string]any{}
	if auth := CurrentAuth(); auth != nil {
		if auth.Data != nil {
			encoded, err := json.Marshal(auth.Data)
			if err != nil {
				return nil, err
			}
			_ = json.Unmarshal(encoded, &principal)
		}
		principal["id"] = auth.UID
		principal["uid"] = auth.UID
		principal["authenticated"] = auth.UID != ""
	} else {
		principal["id"], principal["uid"], principal["authenticated"] = "", "", false
	}
	request := CurrentRequest()
	contextValue := map[string]any{"caller_binding": policy.BindingAddress}
	if request != nil {
		contextValue["method"], contextValue["path"] = request.Method, request.Path
	}
	if tenant, ok := principal["tenant_id"]; ok {
		contextValue["tenant_id"] = tenant
	}
	inputValue, err := simpleContractValue(input)
	if err != nil {
		return nil, err
	}
	return map[string]any{"principal": principal, "context": contextValue, "input": inputValue}, nil
}

func simpleContractValue(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}
