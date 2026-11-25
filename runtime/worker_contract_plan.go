package runtime

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"scenery.sh/internal/contract"
	"scenery.sh/internal/workerabi"
	"scenery.sh/runtime/shared"
)

// WorkerServicePlan maps one canonical service to a supervised process.
// The Host, rather than that process, registers its binding contracts.
type WorkerServicePlan struct {
	Address    string
	Module     string
	Runtime    string
	Name       string
	Process    string
	Operations map[string]WorkerOperationPlan
	Clients    map[string]WorkerClientPlan
	Bindings   []WorkerBindingPlan
	Durable    map[string]WorkerDurablePlan
	Schedules  []WorkerSchedulePlan
	Stores     map[string]string
}

// WorkerStorageHandler serves an invocation-scoped operation on the resolved
// store. The Host has already checked the worker's declared dependency.
type WorkerStorageHandler func(store string, response http.ResponseWriter, request *http.Request)

type WorkerClientPlan struct {
	Binding string
	Enqueue bool
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

type WorkerSchedulePlan struct {
	Address           string
	Operation         string
	Execution         string
	Authorization     string
	Pipeline          string
	Policy            ContractHTTPPolicy
	Input             json.RawMessage
	Timeout           time.Duration
	Every             time.Duration
	Overlap           string
	CatchupMaximumAge time.Duration
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
	Status  int
	Codec   string
	Media   []string
	Body    WorkerResponseValue
	Options ContractResponseOptions
	Stream  bool
	Headers []WorkerResponseHeader
	Cookies []WorkerResponseCookie
}

type WorkerResponseValue struct {
	Path         []string
	Optional     bool
	DefaultEmpty bool
}

type WorkerResponseHeader struct {
	Name     string
	Encoding string
	Value    WorkerResponseValue
}

type WorkerResponseCookie struct {
	Cookie ContractResponseCookie
	Value  WorkerResponseValue
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
	encoded, err := encodeWorkerResponseBody(request, result, response, raw)
	if err != nil {
		return ContractHTTPResponse{}, err
	}
	if err := encodeWorkerResponseMetadata(&encoded, response, raw); err != nil {
		_ = encoded.Close()
		return ContractHTTPResponse{}, ContractSystemError(err)
	}
	return encoded, nil
}

func encodeWorkerResponseBody(request *http.Request, result workerabi.Result, response WorkerResponsePlan, raw json.RawMessage) (ContractHTTPResponse, error) {
	if result.Stream != nil {
		stream := shared.NewByteStream(result.Stream, result.StreamSize)
		return EncodeContractByteStreamWithOptions(request, response.Status, stream, response.Media, response.Options)
	}
	if response.Codec == "" {
		return ContractHTTPResponse{Status: response.Status}, nil
	}
	raw, err := selectWorkerResponseValue(raw, response.Body)
	if err != nil {
		return ContractHTTPResponse{}, ContractSystemError(err)
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

func encodeWorkerResponseMetadata(response *ContractHTTPResponse, plan WorkerResponsePlan, raw json.RawMessage) error {
	for _, header := range plan.Headers {
		value, err := selectWorkerResponseValue(raw, header.Value)
		if err != nil {
			return err
		}
		if len(value) != 0 {
			options := ContractResponseValueOptions{Encoding: header.Encoding, EncodeValue: func(any) ([]byte, error) { return value, nil }}
			if err := AddContractResponseHeader(response, header.Name, value, options); err != nil {
				return err
			}
		}
	}
	for _, cookie := range plan.Cookies {
		value, err := selectWorkerResponseValue(raw, cookie.Value)
		if err != nil {
			return err
		}
		if len(value) != 0 {
			options := ContractResponseValueOptions{EncodeValue: func(any) ([]byte, error) { return value, nil }}
			if err := AddContractResponseCookie(response, cookie.Cookie, value, options); err != nil {
				return err
			}
		}
	}
	return nil
}

func selectWorkerResponseValue(raw json.RawMessage, selection WorkerResponseValue) (json.RawMessage, error) {
	for index, field := range selection.Path {
		object, err := contract.DecodeJSONObject(raw)
		if err != nil {
			return nil, err
		}
		raw = object[field]
		if len(raw) != 0 {
			continue
		}
		if index == len(selection.Path)-1 {
			if selection.DefaultEmpty {
				return json.RawMessage(`""`), nil
			}
			if selection.Optional {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("worker outcome has no mapped field %s", field)
	}
	return raw, nil
}

func closeWorkerResult(result workerabi.Result, err error) error {
	if result.Stream != nil {
		_ = result.Stream.Close()
	}
	return err
}
