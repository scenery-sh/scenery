package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"sync/atomic"
	"testing"

	"scenery.sh/errs"
	"scenery.sh/internal/machine"
	"scenery.sh/internal/spec"
)

func TestProcessHostServesLiveCLIRequestsInTheOwningProcess(t *testing.T) {
	restore := replaceGlobalRegistryForTest()
	defer restore()
	useLinkedProcessIdentityForTest(t)
	useProcessLinkForTest(t, &processLinkConfig{Token: processLinkTestToken, Dispatch: processLinkTarget{Network: "unix", Address: "/unused"}})
	if err := RegisterContractCLIBinding(ContractCLIBindingRegistration{
		Address: "house/binding/process_scene_cli", Command: []string{"scene", "process"},
		Invoke: func(ctx context.Context, input []byte) (ContractCLIOutcome, error) {
			return ContractCLIOutcome{Kind: "result", Name: "processed", Payload: input}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	var served atomic.Int32
	owner := &server{}
	socket := serveProcessLinkForTest(t, withProcessGeneration(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		served.Add(1)
		if req.URL.Path != processCLICallPath {
			http.NotFound(w, req)
			return
		}
		owner.handleProcessCLICall(w, req, nil)
	})))
	instance := processGenerationInstance{Network: socket.Network, Address: socket.Address, PID: os.Getpid(), Identity: processInstanceIdentity(CurrentLinkedContractBundle())}
	host, err := newProcessHost(ProcessHostConfig{Name: "house", Fallback: "house_house"}, processLinkTestToken, processHostTestContract)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.publish(processGenerationManifest{
		Generation: 3, ContractRevision: processHostTestContract, Identity: processHostTestBuild(3),
		Processes: map[string]processGenerationInstance{"house_house": instance},
		Bindings:  map[string]string{"house/binding/process_scene_cli": "house_house"},
	}); err != nil {
		t.Fatal(err)
	}
	request := ContractCLIRequest{
		Kind: ContractCLIRequestKind, SchemaRevision: ContractCLIRequestSchemaRevision, SpecRevision: string(spec.CurrentRevision()),
		Producer: machine.Producer{Version: "test", Toolchain: machine.Toolchain{GoVersion: "go1.27.0"}},
		Binding:  "house/binding/process_scene_cli", Input: json.RawMessage(`{"scene_id":"s"}`),
	}
	result, err := host.invokeCLI(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	response := result.Response
	if err := ValidateContractCLIResponse(response, string(spec.CurrentRevision())); err != nil || response.Outcome == nil || string(response.Outcome.Payload) != `{"scene_id":"s"}` {
		t.Fatalf("live response = %#v, %v", response, err)
	}
	if result.Generation != 3 || result.Process != "house_house" || result.BuildInputDigest != processHostTestBuild(3).BuildInputDigest || served.Load() != 1 {
		t.Fatalf("serving identity = %+v (served %d)", result, served.Load())
	}
	if response.Invocation == nil || response.Invocation.InvocationID == "" {
		t.Fatalf("live response has no invocation identity: %#v", response.Invocation)
	}
	request.Binding = "house/binding/missing_cli"
	if _, err := host.invokeCLI(context.Background(), request); errs.Code(err) != errs.NotFound {
		t.Fatalf("unknown binding = %v", err)
	}
}
