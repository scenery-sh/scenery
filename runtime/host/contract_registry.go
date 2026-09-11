package host

import (
	"fmt"
	"maps"

	"scenery.sh/internal/mcpcontract"
	"scenery.sh/internal/nativecall"
	"scenery.sh/internal/nativecompose"
	"scenery.sh/internal/nativeservice"
	"scenery.sh/internal/runtimeapi"
)

const ContractRuntimeABI = nativecompose.RuntimeABI

type ContractRegistration = nativecompose.Registration
type ContractRegistryOptions = nativecompose.Options

type ContractRegistry struct{ *nativecompose.Registry }

var _ runtimeapi.Registry = (*ContractRegistry)(nil)

func NewContractRegistry(options ContractRegistryOptions) (*ContractRegistry, error) {
	registry, err := nativecompose.New(options)
	if err != nil {
		return nil, err
	}
	return &ContractRegistry{Registry: registry}, nil
}

func (registry *ContractRegistry) Seal() error {
	return registry.Registry.Seal(func() nativecompose.Transaction {
		snapshot := snapshotContractRuntimeState()
		return nativecompose.Transaction{
			Rollback: func() { restoreContractRuntimeState(snapshot) },
			Validate: func() error {
				if err := validateContractPagesRegistered(); err != nil {
					return fmt.Errorf("runtime: validate contract pages: %w", err)
				}
				return nil
			},
		}
	})
}

func cloneContractStringMap(values map[string]string) map[string]string {
	clone := make(map[string]string, len(values))
	maps.Copy(clone, values)
	return clone
}

type contractRuntimeSnapshot struct {
	endpoints                 map[string]*Endpoint
	cronJobs                  map[string]*CronJob
	durableTasks              map[string]*DurableTask
	contractDurableExecutions map[string]ContractDurableRegistration
	contractBindings          *nativecall.Registry
	mcpTools                  map[string]MCPToolRegistration
	mcpFederationSpecs        map[string]MCPFederationRegistration
	mcpFederations            map[string]*mcpFederationState
	mcpFederationAssistants   map[string]string
	mcpSecretResolvers        map[string]MCPSecretResolver
	assistantMCPManifests     map[string]mcpcontract.Manifest
	assistants                map[string]AssistantRegistration
	assistantClients          map[string]AssistantClient
	contractCLIBindings       map[string]ContractCLIBindingRegistration
	contractPages             map[string]ContractPageRegistration
	contractEventBuses        map[string]ContractEventBus
	contractEventConsumers    map[string]ContractEventConsumerRegistration
	contractEventEmissions    map[string]ContractEventEmissionRegistration
	services                  nativeservice.Snapshot
}

func snapshotContractRuntimeState() contractRuntimeSnapshot {
	global.mu.RLock()
	defer global.mu.RUnlock()
	return contractRuntimeSnapshot{
		endpoints: cloneContractMap(global.endpoints), cronJobs: cloneContractMap(global.cronJobs), durableTasks: cloneContractMap(global.durableTasks),
		contractDurableExecutions: cloneContractMap(global.contractDurableExecutions), contractBindings: global.contractBindings.Clone(),
		mcpTools:                cloneMCPToolRegistrations(global.mcpTools),
		mcpFederationSpecs:      cloneMCPFederationRegistrations(global.mcpFederationSpecs),
		mcpFederations:          cloneMCPFederationStates(global.mcpFederations),
		mcpFederationAssistants: cloneContractMap(global.mcpFederationAssistants),
		mcpSecretResolvers:      cloneContractMap(global.mcpSecretResolvers),
		assistantMCPManifests:   cloneMCPManifests(global.assistantMCPManifests),
		assistants:              cloneContractMap(global.assistants), assistantClients: cloneContractMap(global.assistantClients),
		contractCLIBindings: cloneContractMap(global.contractCLIBindings),
		contractPages:       cloneContractMap(global.contractPages),
		contractEventBuses:  cloneContractMap(global.contractEventBuses), contractEventConsumers: cloneContractMap(global.contractEventConsumers),
		contractEventEmissions: cloneContractMap(global.contractEventEmissions), services: global.services.Snapshot(),
	}
}

func restoreContractRuntimeState(snapshot contractRuntimeSnapshot) {
	global.mu.Lock()
	defer global.mu.Unlock()
	global.endpoints = snapshot.endpoints
	global.cronJobs = snapshot.cronJobs
	global.durableTasks = snapshot.durableTasks
	global.contractDurableExecutions = snapshot.contractDurableExecutions
	global.contractBindings = snapshot.contractBindings
	global.mcpTools = snapshot.mcpTools
	global.mcpFederationSpecs = snapshot.mcpFederationSpecs
	global.mcpFederations = snapshot.mcpFederations
	global.mcpFederationAssistants = snapshot.mcpFederationAssistants
	global.mcpSecretResolvers = snapshot.mcpSecretResolvers
	global.assistantMCPManifests = snapshot.assistantMCPManifests
	global.assistants = snapshot.assistants
	global.assistantClients = snapshot.assistantClients
	global.contractCLIBindings = snapshot.contractCLIBindings
	global.contractPages = snapshot.contractPages
	global.contractEventBuses = snapshot.contractEventBuses
	global.contractEventConsumers = snapshot.contractEventConsumers
	global.contractEventEmissions = snapshot.contractEventEmissions
	global.services = nativeservice.Restore(snapshot.services)
}

func cloneContractMap[K comparable, V any](values map[K]V) map[K]V {
	clone := make(map[K]V, len(values))
	maps.Copy(clone, values)
	return clone
}

func cloneMCPManifests(values map[string]mcpcontract.Manifest) map[string]mcpcontract.Manifest {
	clone := make(map[string]mcpcontract.Manifest, len(values))
	for address, manifest := range values {
		manifest.Capabilities = append([]mcpcontract.Capability(nil), manifest.Capabilities...)
		for index := range manifest.Capabilities {
			manifest.Capabilities[index].InputSchema = append([]byte(nil), manifest.Capabilities[index].InputSchema...)
			manifest.Capabilities[index].OutputSchema = append([]byte(nil), manifest.Capabilities[index].OutputSchema...)
		}
		manifest.Connections = append([]mcpcontract.Connection(nil), manifest.Connections...)
		for index := range manifest.Connections {
			manifest.Connections[index].Allow = append([]string(nil), manifest.Connections[index].Allow...)
			manifest.Connections[index].Block = append([]string(nil), manifest.Connections[index].Block...)
		}
		clone[address] = manifest
	}
	return clone
}
