package runtime

import (
	"net/http"
	"os"
	"strconv"
)

// A process host answers the public requests of a process-model session. Its
// answers carry the development response identity headers with the meaning
// they have for a single application executable: the build identity of the
// generation that served the request and the process that answered the
// connection. The host verified that the answering service instance belongs to
// that generation, and every instance of a generation has its process identity
// from the generation's build, so a check that compares answers with the
// verified candidate of the development target observes the same identity in
// either runtime. The answering service instance is named beside it.
const (
	processServiceIDHeader             = "X-Scenery-Service-Process-ID"
	processServiceImplementationHeader = "X-Scenery-Service-Implementation-Revision"
)

var processHostIdentityHeaders = [...]string{processIdentityContractHdr, processIdentityImplHeader, processIdentityBuildHeader, processIdentityTargetHeader, processIdentityPIDHeader}

// attestProcessGeneration replaces the identity headers of an answer with the
// build identity of generation and this host process.
func attestProcessGeneration(headers http.Header, generation *processHostGeneration) {
	identity := generation.identity
	for name, value := range map[string]string{
		processIdentityContractHdr: identity.ContractRevision, processIdentityImplHeader: identity.ImplementationRevision,
		processIdentityBuildHeader: identity.BuildInputDigest, processIdentityTargetHeader: identity.GoTarget,
		processIdentityPIDHeader: strconv.Itoa(os.Getpid()),
	} {
		headers.Set(name, value)
		exposeResponseHeader(headers, name)
	}
}

// processHostAttestingWriter attests the generation on an answer of the host's
// own runtime. Before a generation is published no identity is attested, so
// the host's own linked identity is removed rather than reported.
type processHostAttestingWriter struct {
	http.ResponseWriter
	generation *processHostGeneration
	attested   bool
}

func (w *processHostAttestingWriter) attest() {
	if w.attested {
		return
	}
	w.attested = true
	if w.generation != nil {
		w.Header().Set(processGenerationHeader, strconv.FormatUint(w.generation.number, 10))
		attestProcessGeneration(w.Header(), w.generation)
		return
	}
	for _, name := range processHostIdentityHeaders {
		w.Header().Del(name)
	}
}

func (w *processHostAttestingWriter) WriteHeader(status int) {
	w.attest()
	w.ResponseWriter.WriteHeader(status)
}

func (w *processHostAttestingWriter) Write(data []byte) (int, error) {
	w.attest()
	return w.ResponseWriter.Write(data)
}

func (w *processHostAttestingWriter) Flush() {
	w.attest()
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *processHostAttestingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
