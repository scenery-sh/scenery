package vnext

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var reportTokenCounter atomic.Uint64

func TransportDiagnostic(kind, message string) Diagnostic {
	codes := map[string]string{
		"invalid_request":        "SCN8001",
		"revision_conflict":      "SCN8002",
		"failed_precondition":    "SCN8003",
		"capability_unavailable": "SCN8004",
		"permission_denied":      "SCN8005",
	}
	if code := codes[kind]; code != "" {
		return Diagnostic{Code: code, Severity: "error", Message: message}
	}
	return internalDiagnostic("SCN9000", message)
}

func internalDiagnostic(code, _ string) Diagnostic {
	message := "internal tooling failure"
	if definition, ok := DiagnosticDefinitionFor(code); ok {
		message = definition.Meaning
	}
	return Diagnostic{Code: code, Severity: "error", Message: message, ReportToken: newReportToken()}
}

func newReportToken() string {
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		fallback := strconv.FormatInt(time.Now().UnixNano(), 10) + ":" + strconv.FormatUint(reportTokenCounter.Add(1), 10)
		sum := sha256.Sum256([]byte(fallback))
		copy(entropy[:], sum[:])
	}
	return "rpt_" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(entropy[:]))
}
