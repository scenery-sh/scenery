package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

func DecodeContractJSON[T any](request *http.Request) (T, error) {
	return DecodeContractInput[T](request, nil, ContractRequestSchema{Body: &ContractBodyMapping{Codec: "json"}})
}

func DecodeContractInput[T any](request *http.Request, pathValues map[string]string, schema ContractRequestSchema) (T, error) {
	var value T
	err := decodeContractInputInto(request, pathValues, schema, &value)
	return value, err
}

// Keep schema-driven decoding shared across generated input types; only the
// public wrapper needs a distinct typed result for each Go instantiation.
func decodeContractInputInto(request *http.Request, pathValues map[string]string, schema ContractRequestSchema, value any) error {
	if request == nil {
		return contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, "request is required", nil)
	}
	object := map[string]json.RawMessage{}
	for _, mapping := range schema.Mappings {
		values, err := contractMappingValues(request, pathValues, mapping)
		if err != nil {
			return contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, err.Error(), err)
		}
		if len(values) == 0 {
			if mapping.Optional {
				continue
			}
			return contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, fmt.Sprintf("missing %s %q", mapping.Source, mapping.Name), nil)
		}
		raw, err := contractMappedJSON(values, mapping)
		if err != nil {
			return contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, fmt.Sprintf("invalid %s %q: %v", mapping.Source, mapping.Name, err), err)
		}
		if mapping.Target == "" {
			return contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, "non-body mappings require a field target", nil)
		}
		if _, exists := object[mapping.Target]; exists {
			return contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, fmt.Sprintf("input field %q is populated more than once", mapping.Target), nil)
		}
		object[mapping.Target] = raw
	}

	if schema.Body != nil {
		raw, err := decodeContractBody(request, *schema.Body, schema)
		if err != nil {
			return err
		}
		if schema.Body.Target != "" {
			if _, exists := object[schema.Body.Target]; exists {
				return contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, fmt.Sprintf("input field %q is populated more than once", schema.Body.Target), nil)
			}
			object[schema.Body.Target] = raw
		} else if len(schema.Body.Include) == 0 && len(schema.Body.Except) == 0 && len(object) == 0 {
			return unmarshalContractInput(raw, schema, value)
		} else {
			bodyObject, err := decodeContractJSONObject(raw)
			if err != nil {
				return contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, "request body must be an object", err)
			}
			include := stringSet(schema.Body.Include)
			except := stringSet(schema.Body.Except)
			for name, value := range bodyObject {
				if len(include) > 0 && !include[name] {
					return contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, fmt.Sprintf("input field %q is not accepted from the request body", name), nil)
				}
				if except[name] {
					return contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, fmt.Sprintf("input field %q is excluded from the request body", name), nil)
				}
				if _, exists := object[name]; exists {
					return contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, fmt.Sprintf("input field %q is populated more than once", name), nil)
				}
				object[name] = value
			}
		}
	}
	for _, mapping := range schema.ContextMappings {
		if _, exists := object[mapping.Target]; exists {
			return contractRequestError(schema, "system.internal", http.StatusInternalServerError, fmt.Sprintf("context field %q is populated more than once", mapping.Target), nil)
		}
		value, err := contractContextValue(mapping.Source)
		if err != nil {
			return contractRequestError(schema, "system.internal", http.StatusInternalServerError, "resolve trusted request context", err)
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return contractRequestError(schema, "system.internal", http.StatusInternalServerError, "encode trusted request context", err)
		}
		object[mapping.Target] = raw
	}

	raw, err := json.Marshal(object)
	if err != nil {
		return contractRequestError(schema, "system.internal", http.StatusInternalServerError, "assemble request input", err)
	}
	return unmarshalContractInput(raw, schema, value)
}

func unmarshalContractInput(raw []byte, schema ContractRequestSchema, value any) error {
	if schema.Body != nil && schema.Body.Target == "" && schema.Body.DecodeValue != nil {
		if err := schema.Body.DecodeValue(raw, value); err != nil {
			return contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err), err)
		}
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err), err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, "trailing request body value", err)
	}
	return nil
}
