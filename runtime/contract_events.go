package runtime

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"scenery.sh/runtime/shared"
)

type ContractEventMessage struct {
	ID                string            `json:"id,omitempty"`
	BusAddress        string            `json:"bus_address"`
	Channel           string            `json:"channel"`
	ContractAddress   string            `json:"contract_address"`
	ContractVersion   uint32            `json:"contract_version"`
	Guarantee         string            `json:"guarantee,omitempty"`
	OrderingKey       string            `json:"ordering_key,omitempty"`
	DeduplicationKey  string            `json:"deduplication_key,omitempty"`
	Attempts          int               `json:"attempts,omitempty"`
	Backoff           string            `json:"backoff,omitempty"`
	DeadLetterChannel string            `json:"dead_letter_channel,omitempty"`
	Attributes        map[string]string `json:"attributes,omitempty"`
	Payload           []byte            `json:"payload"`
}

type ContractEventSubscription struct {
	Address           string
	BusAddress        string
	Channel           string
	ContractAddress   string
	ContractVersion   uint32
	Guarantee         string
	Attempts          int
	Backoff           string
	DeadLetterChannel string
	Handle            func(context.Context, ContractEventMessage) error
}

type ContractEventBus interface {
	Publish(context.Context, ContractEventMessage) error
	Subscribe(context.Context, ContractEventSubscription) (func(context.Context) error, error)
}

type ContractEventConsumerRegistration struct {
	Address           string
	BusAddress        string
	Channel           string
	ContractAddress   string
	ContractVersion   uint32
	Guarantee         string
	Identity          string
	Attempts          int
	Backoff           string
	DeadLetterChannel string
	Policy            *ContractHTTPPolicy
	Invoke            func(context.Context, []byte) error
}

type ContractEventEmissionRegistration struct {
	Address           string
	OperationAddress  string
	BusAddress        string
	Channel           string
	ContractAddress   string
	ContractVersion   uint32
	Guarantee         string
	Attempts          int
	Backoff           string
	DeadLetterChannel string
	Encode            func(any) ([]byte, bool, error)
	OrderingKey       func(any) (string, error)
	DeduplicationKey  func(any) (string, error)
}

type ContractEventRuntime struct {
	stops []func(context.Context) error
}

func RegisterContractEventBus(address string, bus ContractEventBus) error {
	address = strings.TrimSpace(address)
	if address == "" || bus == nil {
		return fmt.Errorf("runtime: contract event bus requires an address and implementation")
	}
	global.mu.Lock()
	defer global.mu.Unlock()
	if global.contractEventBuses == nil {
		global.contractEventBuses = make(map[string]ContractEventBus)
	}
	if _, exists := global.contractEventBuses[address]; exists {
		return fmt.Errorf("runtime: duplicate contract event bus %s", address)
	}
	global.contractEventBuses[address] = bus
	return nil
}

func RegisterContractEventConsumer(registration ContractEventConsumerRegistration) error {
	registration.Address = strings.TrimSpace(registration.Address)
	registration.BusAddress = strings.TrimSpace(registration.BusAddress)
	registration.Channel = strings.TrimSpace(registration.Channel)
	registration.ContractAddress = strings.TrimSpace(registration.ContractAddress)
	registration.Identity = strings.TrimSpace(registration.Identity)
	registration.Guarantee = strings.TrimSpace(registration.Guarantee)
	registration.Backoff = strings.TrimSpace(registration.Backoff)
	registration.DeadLetterChannel = strings.TrimSpace(registration.DeadLetterChannel)
	registration.Backoff = strings.TrimSpace(registration.Backoff)
	if registration.Address == "" || registration.BusAddress == "" || registration.Channel == "" || registration.ContractAddress == "" || registration.Identity == "" || registration.ContractVersion == 0 || registration.Invoke == nil {
		return fmt.Errorf("runtime: contract event consumer requires address, bus, channel, contract, version, identity, and invoke")
	}
	if !validContractEventGuarantee(registration.Guarantee) || registration.Attempts <= 0 || !validContractEventBackoff(registration.Backoff) {
		return fmt.Errorf("runtime: contract event consumer %s has invalid delivery policy", registration.Address)
	}
	if err := validateContractHTTPPolicy(registration.Policy); err != nil {
		return fmt.Errorf("runtime: contract event consumer %s policy: %w", registration.Address, err)
	}
	global.mu.Lock()
	defer global.mu.Unlock()
	if global.contractEventConsumers == nil {
		global.contractEventConsumers = make(map[string]ContractEventConsumerRegistration)
	}
	if _, exists := global.contractEventConsumers[registration.Address]; exists {
		return fmt.Errorf("runtime: duplicate contract event consumer %s", registration.Address)
	}
	global.contractEventConsumers[registration.Address] = registration
	return nil
}

func RegisterContractEventEmission(registration ContractEventEmissionRegistration) error {
	registration.Address = strings.TrimSpace(registration.Address)
	registration.OperationAddress = strings.TrimSpace(registration.OperationAddress)
	registration.BusAddress = strings.TrimSpace(registration.BusAddress)
	registration.Channel = strings.TrimSpace(registration.Channel)
	registration.ContractAddress = strings.TrimSpace(registration.ContractAddress)
	registration.Guarantee = strings.TrimSpace(registration.Guarantee)
	if registration.Address == "" || registration.OperationAddress == "" || registration.BusAddress == "" || registration.Channel == "" || registration.ContractAddress == "" || registration.ContractVersion == 0 || registration.Encode == nil {
		return fmt.Errorf("runtime: contract event emission requires address, operation, bus, channel, contract, version, and encoder")
	}
	if !validContractEventGuarantee(registration.Guarantee) {
		return fmt.Errorf("runtime: contract event emission %s has invalid guarantee", registration.Address)
	}
	if registration.Attempts == 0 {
		registration.Attempts = 1
	}
	if registration.Backoff == "" {
		registration.Backoff = "none"
	}
	if registration.Attempts <= 0 || !validContractEventBackoff(registration.Backoff) {
		return fmt.Errorf("runtime: contract event emission %s has invalid broker retry policy", registration.Address)
	}
	global.mu.Lock()
	defer global.mu.Unlock()
	if global.contractEventEmissions == nil {
		global.contractEventEmissions = make(map[string]ContractEventEmissionRegistration)
	}
	if _, exists := global.contractEventEmissions[registration.Address]; exists {
		return fmt.Errorf("runtime: duplicate contract event emission %s", registration.Address)
	}
	global.contractEventEmissions[registration.Address] = registration
	return nil
}

func StartContractEventRuntime(ctx context.Context) (*ContractEventRuntime, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	global.mu.RLock()
	buses := make(map[string]ContractEventBus, len(global.contractEventBuses))
	for address, bus := range global.contractEventBuses {
		buses[address] = bus
	}
	consumers := make([]ContractEventConsumerRegistration, 0, len(global.contractEventConsumers))
	for _, consumer := range global.contractEventConsumers {
		consumers = append(consumers, consumer)
	}
	global.mu.RUnlock()
	sort.Slice(consumers, func(i, j int) bool { return consumers[i].Address < consumers[j].Address })
	running := &ContractEventRuntime{}
	for _, consumer := range consumers {
		bus := buses[consumer.BusAddress]
		if bus == nil {
			_ = running.Stop(context.Background())
			return nil, fmt.Errorf("capability_unavailable: event bus %s for %s is not registered", consumer.BusAddress, consumer.Address)
		}
		consumer := consumer
		subscription := ContractEventSubscription{
			Address: consumer.Address, BusAddress: consumer.BusAddress, Channel: consumer.Channel,
			ContractAddress: consumer.ContractAddress, ContractVersion: consumer.ContractVersion,
			Guarantee: consumer.Guarantee, Attempts: consumer.Attempts, Backoff: consumer.Backoff, DeadLetterChannel: consumer.DeadLetterChannel,
			Handle: func(callCtx context.Context, message ContractEventMessage) error {
				if message.BusAddress != consumer.BusAddress || message.Channel != consumer.Channel || message.ContractAddress != consumer.ContractAddress || message.ContractVersion != consumer.ContractVersion {
					return fmt.Errorf("runtime: event message contradicts subscription %s", consumer.Address)
				}
				callCtx, restore := enterContractEventInvocation(callCtx, consumer, message)
				defer restore()
				return consumer.Invoke(callCtx, append([]byte(nil), message.Payload...))
			},
		}
		stop, err := bus.Subscribe(ctx, subscription)
		if err != nil {
			_ = running.Stop(context.Background())
			return nil, fmt.Errorf("runtime: subscribe contract event consumer %s: %w", consumer.Address, err)
		}
		if stop != nil {
			running.stops = append(running.stops, stop)
		}
	}
	return running, nil
}

func (running *ContractEventRuntime) Stop(ctx context.Context) error {
	if running == nil {
		return nil
	}
	var errors []error
	for index := len(running.stops) - 1; index >= 0; index-- {
		if err := running.stops[index](ctx); err != nil {
			errors = append(errors, err)
		}
	}
	running.stops = nil
	return errorsJoin(errors...)
}

func PublishContractOperationOutcome(ctx context.Context, operationAddress string, outcome any) error {
	operationAddress = strings.TrimSpace(operationAddress)
	global.mu.RLock()
	registrations := make([]ContractEventEmissionRegistration, 0)
	for _, registration := range global.contractEventEmissions {
		if registration.OperationAddress == operationAddress {
			registrations = append(registrations, registration)
		}
	}
	buses := make(map[string]ContractEventBus, len(global.contractEventBuses))
	for address, bus := range global.contractEventBuses {
		buses[address] = bus
	}
	global.mu.RUnlock()
	sort.Slice(registrations, func(i, j int) bool { return registrations[i].Address < registrations[j].Address })
	for _, registration := range registrations {
		payload, matched, err := registration.Encode(outcome)
		if err != nil {
			return fmt.Errorf("runtime: encode event emission %s: %w", registration.Address, err)
		}
		if !matched {
			continue
		}
		bus := buses[registration.BusAddress]
		if bus == nil {
			return fmt.Errorf("capability_unavailable: event bus %s for %s is not registered", registration.BusAddress, registration.Address)
		}
		message := ContractEventMessage{
			ID: uuid.NewString(), BusAddress: registration.BusAddress, Channel: registration.Channel,
			ContractAddress: registration.ContractAddress, ContractVersion: registration.ContractVersion,
			Guarantee: registration.Guarantee, Attempts: registration.Attempts, Backoff: registration.Backoff,
			DeadLetterChannel: registration.DeadLetterChannel, Payload: append([]byte(nil), payload...),
		}
		if registration.OrderingKey != nil {
			message.OrderingKey, err = registration.OrderingKey(outcome)
			if err != nil {
				return fmt.Errorf("runtime: event emission %s ordering key: %w", registration.Address, err)
			}
		}
		if registration.DeduplicationKey != nil {
			message.DeduplicationKey, err = registration.DeduplicationKey(outcome)
			if err != nil {
				return fmt.Errorf("runtime: event emission %s deduplication key: %w", registration.Address, err)
			}
		}
		if err := bus.Publish(ctx, message); err != nil {
			return fmt.Errorf("runtime: publish event emission %s: %w", registration.Address, err)
		}
	}
	return nil
}

func enterContractEventInvocation(ctx context.Context, consumer ContractEventConsumerRegistration, message ContractEventMessage) (context.Context, func()) {
	if ctx == nil {
		ctx = context.Background()
	}
	started := time.Now().UTC()
	invocationID := message.ID
	if invocationID == "" {
		invocationID = uuid.NewString()
	}
	state := &requestState{
		started:     started,
		request:     shared.Request{Type: shared.EventCall, Started: started, InvocationID: invocationID, Service: "event", Endpoint: consumer.Address, Method: "EVENT", Path: consumer.Channel, Headers: make(http.Header), Payload: append([]byte(nil), message.Payload...), CallerBinding: consumer.Address},
		auth:        AuthInfo{UID: consumer.Identity, Data: map[string]any{"workload_identity": consumer.Identity, "event_contract": consumer.ContractAddress}},
		logsEnabled: true, traceEnabled: true,
	}
	ctx = withState(ctx, state)
	ctx = withRuntimeInvocation(ctx, state)
	restore := enterState(state)
	return ctx, restore
}

func validContractEventGuarantee(value string) bool {
	switch value {
	case "at_most_once", "at_least_once", "exactly_once":
		return true
	default:
		return false
	}
}

func validContractEventBackoff(value string) bool {
	switch value {
	case "none", "fixed", "exponential":
		return true
	default:
		return false
	}
}
