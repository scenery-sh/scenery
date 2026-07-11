package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type ContractWorkloadIdentity struct {
	Address       string
	Issuer        string
	PrincipalType string
	ClaimsJSON    string
}

func (identity ContractWorkloadIdentity) Mint() (AuthInfo, error) {
	identity.Address = strings.TrimSpace(identity.Address)
	identity.Issuer = strings.TrimSpace(identity.Issuer)
	identity.PrincipalType = strings.TrimSpace(identity.PrincipalType)
	if identity.Address == "" || identity.Issuer == "" || identity.PrincipalType == "" {
		return AuthInfo{}, fmt.Errorf("runtime: workload identity requires address, issuer, and principal type")
	}
	if identity.Issuer != "std.identity_issuer.runtime" {
		return AuthInfo{}, fmt.Errorf("capability_unavailable: workload identity issuer %s is unavailable", identity.Issuer)
	}
	if identity.PrincipalType != "std.type.workload_principal" {
		return AuthInfo{}, fmt.Errorf("capability_unavailable: workload principal type %s is unavailable", identity.PrincipalType)
	}
	claims := map[string]any{}
	if strings.TrimSpace(identity.ClaimsJSON) != "" {
		decoder := json.NewDecoder(bytes.NewBufferString(identity.ClaimsJSON))
		decoder.UseNumber()
		if err := decoder.Decode(&claims); err != nil {
			return AuthInfo{}, fmt.Errorf("runtime: workload identity claims: %w", err)
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			return AuthInfo{}, fmt.Errorf("runtime: workload identity claims contain trailing JSON")
		}
	}
	if claims == nil {
		return AuthInfo{}, fmt.Errorf("runtime: workload identity claims must be an object")
	}
	for _, reserved := range []string{"issuer", "principal_type", "workload_identity"} {
		if _, exists := claims[reserved]; exists {
			return AuthInfo{}, fmt.Errorf("runtime: workload identity claim %q is reserved", reserved)
		}
	}
	claims["issuer"] = identity.Issuer
	claims["principal_type"] = identity.PrincipalType
	claims["workload_identity"] = identity.Address
	return AuthInfo{UID: "workload:" + identity.Address, Data: claims}, nil
}
