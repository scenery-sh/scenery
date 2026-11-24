package runtime

import (
	"encoding/json"
	"fmt"
	"net/http"

	"scenery.sh/internal/contract"
	"scenery.sh/internal/workerabi"
	"scenery.sh/runtime/shared"
)

// WorkerServicePlan maps one canonical service to a supervised process.
// The Host, rather than that process, registers its binding contracts.
type WorkerServicePlan struct {
	Address    string
	Module     string
	Name       string
	Process    string
	Operations map[string]WorkerOperationPlan
	Clients    map[string]string
	Bindings   []WorkerBindingPlan
	Durable    map[string]WorkerDurablePlan
}

type WorkerOperationPlan struct {
	Input    string
	Outcomes map[string]string
}

type WorkerBindingPlan struct {
	Address    string
	Operation  string
	Policy     ContractHTTPPolicy
	HTTP       *WorkerHTTPPlan
	Visibility string
	Delivery   string
	Execution  string
}

type WorkerDurablePlan struct {
	Operation    string
	Registration ContractDurableRegistration
	Dedupe       []WorkerKeyField
	Concurrency  []WorkerKeyField
}

type WorkerKeyField struct {
	Name string
	Type string
}

type WorkerHTTPPlan struct {
	Method    string
	Path      string
	Access    Access
	PathTail  *ContractPathTail
	Request   ContractRequestSchema
	Responses map[string]WorkerResponsePlan
}

type WorkerResponsePlan struct {
	Status   int
	Codec    string
	Media    []string
	BodyPath []string
	Options  ContractResponseOptions
	Stream   bool
}

func decodeWorkerRequest(request *http.Request, path map[string]string, plan ContractRequestSchema, normalize func([]byte) ([]byte, error)) (ContractDecodedRequest, error) {
	input, err := DecodeContractInput[json.RawMessage](request, path, plan)
	if err != nil {
		return ContractDecodedRequest{}, err
	}
	canonical, err := normalize(input)
	if err != nil {
		return ContractDecodedRequest{}, contractRequestError(plan, "transport.invalid_request", http.StatusBadRequest, "request does not match the declared input", err)
	}
	return ContractDecodedRequest{Payload: json.RawMessage(canonical)}, nil
}

func encodeWorkerResponse(request *http.Request, result workerabi.Result, responses map[string]WorkerResponsePlan) (ContractHTTPResponse, error) {
	kind, name, raw, err := contract.DecodeContractOutcomeEnvelope(result.Outcome)
	if err != nil {
		return ContractHTTPResponse{}, ContractSystemError(closeWorkerResult(result, err))
	}
	response, ok := responses[kind+"."+name]
	if kind == "result" && name == "dispatch.enqueued" {
		response, ok = responses[name]
	}
	if !ok || response.Stream != (result.Stream != nil) {
		return ContractHTTPResponse{}, ContractSystemError(closeWorkerResult(result, fmt.Errorf("worker returned an unmapped outcome or streaming mode")))
	}
	if result.Stream != nil {
		stream := shared.NewByteStream(result.Stream, result.StreamSize)
		return EncodeContractByteStreamWithOptions(request, response.Status, stream, response.Media, response.Options)
	}
	if response.Codec == "" {
		return ContractHTTPResponse{Status: response.Status}, nil
	}
	for _, field := range response.BodyPath {
		object, err := contract.DecodeJSONObject(raw)
		if err != nil {
			return ContractHTTPResponse{}, ContractSystemError(err)
		}
		raw = object[field]
		if len(raw) == 0 {
			return ContractHTTPResponse{}, ContractSystemError(fmt.Errorf("worker outcome has no mapped field %s", field))
		}
	}
	if response.Codec == "json" || response.Codec == "problem_json" {
		accept, acceptEncoding := contractNegotiationHeaders(request)
		media, err := negotiateContractMedia(accept, response.Media)
		if err != nil {
			return ContractHTTPResponse{}, &ContractTransportError{Outcome: "transport.not_acceptable", Status: http.StatusNotAcceptable, Message: err.Error(), Cause: err}
		}
		return finishContractRepresentation(response.Status, media, acceptEncoding, raw, response.Options)
	}
	switch response.Codec {
	case "text":
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return ContractHTTPResponse{}, ContractSystemError(err)
		}
		return EncodeContractRepresentationWithOptions(request, response.Status, value, response.Codec, response.Media, response.Options)
	case "bytes":
		var value []byte
		if err := json.Unmarshal(raw, &value); err != nil {
			return ContractHTTPResponse{}, ContractSystemError(err)
		}
		return EncodeContractRepresentationWithOptions(request, response.Status, value, response.Codec, response.Media, response.Options)
	default:
		return ContractHTTPResponse{}, ContractSystemError(fmt.Errorf("unknown worker response codec %s", response.Codec))
	}
}

func closeWorkerResult(result workerabi.Result, err error) error {
	if result.Stream != nil {
		_ = result.Stream.Close()
	}
	return err
}
