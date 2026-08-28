package runtime

import (
	"net/http"

	"scenery.sh/internal/contract"
)

// ContractPreparedOutcome carries one typed operation outcome together with
// the canonical outcome-envelope bytes that produced its isolation clone.
// Generated HTTP adapters return it from Invoke so the response encoder can
// reuse the already-produced canonical payload instead of marshaling the
// outcome a second time.
type ContractPreparedOutcome struct {
	Outcome  any
	Envelope []byte
}

// NewContractPreparedOutcome wraps a cloned outcome with the canonical
// envelope bytes it was decoded from.
func NewContractPreparedOutcome(outcome any, envelope []byte) *ContractPreparedOutcome {
	return &ContractPreparedOutcome{Outcome: outcome, Envelope: envelope}
}

// ContractPreparedOutcomeValue unwraps a prepared outcome for the generated
// encode switch, returning the typed outcome and the envelope bytes when
// present. Plain outcomes pass through unchanged with a nil envelope.
func ContractPreparedOutcomeValue(outcome any) (any, []byte) {
	if prepared, ok := outcome.(*ContractPreparedOutcome); ok && prepared != nil {
		return prepared.Outcome, prepared.Envelope
	}
	return outcome, nil
}

// EncodeContractPreparedRepresentationWithOptions encodes one typed outcome
// body whose value is the whole outcome payload. When the canonical outcome
// envelope is available its payload bytes are reused as the response body —
// they are byte-identical to what the schema-directed encoder would produce
// for the cloned value — and otherwise the ordinary encoder runs.
func EncodeContractPreparedRepresentationWithOptions(request *http.Request, status int, envelope []byte, value any, codec string, produced []string, options ContractResponseOptions) (ContractHTTPResponse, error) {
	if len(envelope) > 0 && (codec == "json" || codec == "problem_json") {
		if _, _, payload, err := contract.DecodeContractOutcomeEnvelope(envelope); err == nil {
			accept, acceptEncoding := contractNegotiationHeaders(request)
			mediaType, negotiateErr := negotiateContractMedia(accept, produced)
			if negotiateErr != nil {
				return ContractHTTPResponse{}, &ContractTransportError{Outcome: "transport.not_acceptable", Status: http.StatusNotAcceptable, Message: negotiateErr.Error(), Cause: negotiateErr}
			}
			return finishContractRepresentation(status, mediaType, acceptEncoding, []byte(payload), options)
		}
	}
	return EncodeContractRepresentationWithOptions(request, status, value, codec, produced, options)
}
