package contractagent

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/evolution"
	"scenery.sh/internal/graph"
)

var reportTokenCounter atomic.Uint64

func newReportToken() string {
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		fallback := strconv.FormatInt(time.Now().UnixNano(), 10) + ":" + strconv.FormatUint(reportTokenCounter.Add(1), 10)
		sum := sha256.Sum256([]byte(fallback))
		copy(entropy[:], sum[:])
	}
	return "rpt_" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(entropy[:]))
}

type Result = compiler.Result
type Manifest = graph.Manifest
type Resource = graph.Resource
type Diagnostic = graph.Diagnostic
type Origin = graph.Origin
type ContextOptions = graph.ContextOptions
type SemanticOperation = evolution.SemanticOperation
type ChangeRequest = evolution.ChangeRequest
type ChangePlan = evolution.ChangePlan
type ApprovalToken = evolution.ApprovalToken
type ApprovalVerifier = evolution.ApprovalVerifier
type ApplyOptions = evolution.ApplyOptions

const (
	agentMaxResources = graph.AgentMaxResources
	agentMaxBytes     = graph.AgentMaxBytes
)

func resourcesByAddress(manifest *Manifest) map[string]Resource {
	result := map[string]Resource{}
	if manifest != nil {
		for _, resource := range manifest.Resources {
			result[resource.Address] = resource
		}
	}
	return result
}

func canonicalStrings(values []string) []string {
	set := map[string]bool{}
	for _, value := range values {
		if value != "" {
			set[value] = true
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
