package runtime

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"scenery.sh/errs"
)

// A process host of a development session can fail selected work on purpose, so
// a disposable session can prove how an application behaves when a service
// refuses a call, answers past its deadline, or loses its answer after the call
// was delivered. Rules are owned by the supervisor's private control listener,
// apply a bounded number of times, and never exist outside this session.

const processFaultsPath = "/__scenery/process/v1/faults"

const (
	processFaultRefuse = "refuse"
	processFaultDelay  = "delay"
	processFaultAbort  = "abort"
)

// ProcessHostFault matches the work it fails. An empty selector matches every
// value of that kind; Count bounds how often the rule applies.
type processHostFault struct {
	Process string `json:"process,omitempty"`
	Binding string `json:"binding,omitempty"`
	Path    string `json:"path,omitempty"`
	Mode    string `json:"mode"`
	DelayMS int    `json:"delay_ms,omitempty"`
	Count   int    `json:"count,omitempty"`
	Message string `json:"message,omitempty"`
}

type processHostFaults struct {
	sync.Mutex
	rules []processHostFault
}

func (faults *processHostFaults) replace(rules []processHostFault) error {
	for index, rule := range rules {
		switch rule.Mode {
		case processFaultRefuse, processFaultAbort:
		case processFaultDelay:
			if rule.DelayMS <= 0 {
				return fmt.Errorf("fault %d delays without a duration", index)
			}
		default:
			return fmt.Errorf("fault %d has unsupported mode %q", index, rule.Mode)
		}
		if rule.Count <= 0 {
			rules[index].Count = 1
		}
	}
	faults.Lock()
	defer faults.Unlock()
	faults.rules = rules
	return nil
}

func (faults *processHostFaults) list() []processHostFault {
	faults.Lock()
	defer faults.Unlock()
	return append([]processHostFault(nil), faults.rules...)
}

// takeControl consumes the first rule that names a prefix of a generation
// control path and no service process or binding.
func (faults *processHostFaults) takeControl(path string) processHostFault {
	faults.Lock()
	defer faults.Unlock()
	for index := range faults.rules {
		rule := faults.rules[index]
		if rule.Count > 0 && rule.Process == "" && rule.Binding == "" && strings.HasPrefix(rule.Path, processGenerationsPath) && strings.HasPrefix(path, rule.Path) {
			faults.rules[index].Count--
			return rule
		}
	}
	return processHostFault{}
}

// take consumes the first rule that matches one piece of work; the empty mode
// means no rule applied. A rule for generation control never applies to work.
func (faults *processHostFaults) take(process, binding, path string) processHostFault {
	faults.Lock()
	defer faults.Unlock()
	for index := range faults.rules {
		rule := faults.rules[index]
		if rule.Count <= 0 || strings.HasPrefix(rule.Path, processGenerationsPath) || rule.Process != "" && rule.Process != process ||
			rule.Binding != "" && rule.Binding != binding ||
			rule.Path != "" && !strings.HasPrefix(path, rule.Path) {
			continue
		}
		faults.rules[index].Count--
		return rule
	}
	return processHostFault{}
}

func (h *processHost) serveFaults(w http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodGet {
		writeProcessHostJSON(w, http.StatusOK, map[string]any{"faults": h.faults.list()})
		return
	}
	var body struct {
		Faults []processHostFault `json:"faults"`
	}
	decoder := json.NewDecoder(io.LimitReader(req.Body, processHostManifestMaxBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		http.Error(w, "malformed fault rules", http.StatusBadRequest)
		return
	}
	if err := h.faults.replace(body.Faults); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// applyProcessFault fails one dispatched internal call on purpose. It reports
// whether the call was answered.
func (h *processHost) applyProcessFault(w http.ResponseWriter, req *http.Request, process, binding string) bool {
	rule := h.faults.take(process, binding, req.URL.Path)
	switch rule.Mode {
	case processFaultRefuse:
		writeProcessLinkUnavailable(w, processFaultMessage(rule, "service process "+process+" refused the call"), "not_sent")
		return true
	case processFaultAbort:
		writeProcessLinkUnavailable(w, processFaultMessage(rule, "service process "+process+" lost the answer"), "unknown")
		return true
	case processFaultDelay:
		if !sleepProcessFault(req, rule) {
			writeProcessLinkUnavailable(w, processFaultMessage(rule, "service process "+process+" did not answer in time"), "unknown")
			return true
		}
	}
	return false
}

// applyControlFault fails one generation control request of the supervisor on
// purpose: refuse answers without acting, delay answers late, and abort acts
// and then loses the answer. It reports whether the request was answered.
func (h *processHost) applyControlFault(w http.ResponseWriter, req *http.Request, serve func(http.ResponseWriter, *http.Request)) bool {
	rule := h.faults.takeControl(req.URL.Path)
	switch rule.Mode {
	case processFaultRefuse:
		http.Error(w, processFaultMessage(rule, "process host refused the control request"), http.StatusServiceUnavailable)
		return true
	case processFaultDelay:
		_ = sleepProcessFault(req, rule)
	case processFaultAbort:
		serve(&processHostDiscardingWriter{header: http.Header{}}, req)
		if connection, _, err := http.NewResponseController(w).Hijack(); err == nil {
			_ = connection.Close()
		}
		return true
	}
	return false
}

// processHostDiscardingWriter lets a control request act without answering.
type processHostDiscardingWriter struct{ header http.Header }

func (w *processHostDiscardingWriter) Header() http.Header            { return w.header }
func (w *processHostDiscardingWriter) Write(data []byte) (int, error) { return len(data), nil }
func (w *processHostDiscardingWriter) WriteHeader(int)                {}

// applyIngressFault fails one forwarded request on purpose.
func (h *processHost) applyIngressFault(w http.ResponseWriter, req *http.Request, process string) bool {
	rule := h.faults.take(process, "", req.URL.EscapedPath())
	switch rule.Mode {
	case processFaultRefuse, processFaultAbort:
		errs.HTTPErrorWithCode(w, errs.B().Code(errs.Unavailable).Msg(processFaultMessage(rule, fmt.Sprintf(processHostUnavailableReason, process))).Err(), http.StatusServiceUnavailable)
		return true
	case processFaultDelay:
		if !sleepProcessFault(req, rule) {
			errs.HTTPErrorWithCode(w, errs.B().Code(errs.Unavailable).Msg(processFaultMessage(rule, fmt.Sprintf(processHostUnavailableReason, process))).Err(), http.StatusServiceUnavailable)
			return true
		}
	}
	return false
}

// sleepProcessFault delays work and reports whether the caller is still waiting.
func sleepProcessFault(req *http.Request, rule processHostFault) bool {
	timer := time.NewTimer(time.Duration(rule.DelayMS) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-req.Context().Done():
		return false
	}
}

func processFaultMessage(rule processHostFault, fallback string) string {
	if strings.TrimSpace(rule.Message) != "" {
		return rule.Message
	}
	return fallback
}
