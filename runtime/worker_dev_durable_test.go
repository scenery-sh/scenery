package runtime

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkerDevDurableTargetResolution(t *testing.T) {
	t.Parallel()
	execution := WorkerDurablePlan{
		Operation: "inventory/operation/restock",
		Registration: ContractDurableRegistration{
			Address: "inventory/execution/stock_job", ExternalName: "stock.replenish",
		},
	}
	workers := &workerHost{services: map[string]WorkerServicePlan{
		"stock": {
			Module: "inventory", Durable: map[string]WorkerDurablePlan{execution.Registration.Address: execution},
			Schedules: []WorkerSchedulePlan{{Address: "inventory/schedule/nightly", Operation: execution.Operation, Execution: execution.Registration.Address}},
		},
	}}
	for _, alias := range []string{"inventory.restock", "inventory.stock_job", execution.Registration.Address, "stock.replenish"} {
		target, err := workers.resolveDevDurableTarget(alias)
		if err != nil || target.address != execution.Registration.Address || target.kind != "durable" {
			t.Fatalf("resolve %q = %+v, %v", alias, target, err)
		}
	}
	if target, err := workers.resolveDevDurableTarget("nightly"); err != nil || target.kind != "schedule" || target.execution.Operation != execution.Operation {
		t.Fatalf("resolve schedule = %+v, %v", target, err)
	}
	if _, err := workers.resolveDevDurableTarget("unknown"); err == nil || !strings.Contains(err.Error(), "stock.replenish") {
		t.Fatalf("unknown error = %v", err)
	}
	workers.services["other"] = WorkerServicePlan{
		Module: "other", Schedules: []WorkerSchedulePlan{{Address: "other/schedule/nightly"}},
	}
	if _, err := workers.resolveDevDurableTarget("nightly"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguity = %v", err)
	}
}

func TestWorkerDevDurableControlRequiresDevToken(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		dev, authenticated bool
		status             int
	}{
		{true, false, http.StatusUnauthorized},
		{false, true, http.StatusNotFound},
		{true, true, http.StatusBadRequest},
	} {
		host := &processHost{token: "private", workers: &workerHost{}, developmentCall: test.dev}
		req := httptest.NewRequest(http.MethodPost, "/__scenery/process/v1/dev-durable", strings.NewReader("{"))
		if test.authenticated {
			req.Header.Set("Authorization", "Bearer private")
		}
		response := httptest.NewRecorder()
		host.serveControl(response, req)
		if response.Code != test.status {
			t.Errorf("status = %d, want %d (%s)", response.Code, test.status, response.Body.String())
		}
	}
}
