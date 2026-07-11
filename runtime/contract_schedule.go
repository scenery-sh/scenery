package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type ContractScheduleRegistration struct {
	Address              string
	Name                 string
	TriggerKind          string
	TriggerValue         string
	Timezone             string
	Overlap              string
	CatchupMaximumAge    time.Duration
	Identity             ContractWorkloadIdentity
	AuthorizationAddress string
	PipelineAddress      string
	Policy               *ContractHTTPPolicy
	Invoke               func(context.Context) error
}

func RegisterContractSchedule(registration ContractScheduleRegistration) error {
	registration.Address = strings.TrimSpace(registration.Address)
	registration.Name = strings.TrimSpace(registration.Name)
	registration.TriggerKind = strings.TrimSpace(registration.TriggerKind)
	registration.TriggerValue = strings.TrimSpace(registration.TriggerValue)
	registration.AuthorizationAddress = strings.TrimSpace(registration.AuthorizationAddress)
	registration.PipelineAddress = strings.TrimSpace(registration.PipelineAddress)
	if registration.Address == "" || registration.Name == "" || registration.TriggerKind == "" || registration.TriggerValue == "" || registration.Identity.Address == "" || registration.AuthorizationAddress == "" || registration.PipelineAddress == "" || registration.Invoke == nil {
		return fmt.Errorf("runtime: contract schedule requires address, name, trigger, identity, authorization, pipeline, and invoke")
	}
	if _, err := registration.Identity.Mint(); err != nil {
		return fmt.Errorf("runtime: contract schedule %s identity: %w", registration.Address, err)
	}
	if err := validateContractHTTPPolicy(registration.Policy); err != nil {
		return fmt.Errorf("runtime: contract schedule %s policy: %w", registration.Address, err)
	}
	job := &CronJob{
		ID: contractScheduleID(registration.Address, registration.Name), Title: registration.Address,
		OverlapPolicy: contractScheduleOverlap(registration.Overlap), CatchupWindow: registration.CatchupMaximumAge,
		Invoke: func(ctx context.Context) error {
			state := stateFromContext(ctx)
			if state != nil {
				auth, err := registration.Identity.Mint()
				if err != nil {
					return err
				}
				claims, _ := auth.Data.(map[string]any)
				claims["authorization"] = registration.AuthorizationAddress
				claims["pipeline"] = registration.PipelineAddress
				auth.Data = claims
				state.auth = auth
				ctx = withRuntimeInvocation(ctx, state)
			}
			return registration.Invoke(ctx)
		},
	}
	switch registration.TriggerKind {
	case "cron":
		job.Schedule, job.Timezone = registration.TriggerValue, defaultStringRuntime(registration.Timezone, "UTC")
	case "every":
		value, err := time.ParseDuration(registration.TriggerValue)
		if err != nil || value <= 0 {
			return fmt.Errorf("runtime: contract schedule %s every trigger is invalid", registration.Address)
		}
		job.Every = value
	case "at":
		value, err := time.Parse(time.RFC3339Nano, registration.TriggerValue)
		if err != nil {
			return fmt.Errorf("runtime: contract schedule %s at trigger is invalid", registration.Address)
		}
		job.At = value
	case "calendar":
		job.Calendar, job.Timezone = registration.TriggerValue, defaultStringRuntime(registration.Timezone, "UTC")
	default:
		return fmt.Errorf("capability_unavailable: contract schedule trigger %s is not supported", registration.TriggerKind)
	}
	return RegisterCronJobChecked(job)
}

func contractScheduleID(address, name string) string {
	value := strings.ToLower(strings.TrimSpace(name))
	var result strings.Builder
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' {
			result.WriteRune(character)
		} else {
			result.WriteByte('-')
		}
	}
	value = strings.Trim(result.String(), "-")
	if len(value) > 48 {
		value = value[:48]
	}
	if value == "" {
		value = "schedule"
	}
	return value + "-" + shortContractAddressHash(address)
}

func shortContractAddressHash(value string) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz234567"
	var hash uint64 = 1469598103934665603
	for index := 0; index < len(value); index++ {
		hash ^= uint64(value[index])
		hash *= 1099511628211
	}
	var encoded [8]byte
	for index := range encoded {
		encoded[index] = alphabet[hash&31]
		hash >>= 5
	}
	return string(encoded[:])
}

func contractScheduleOverlap(value string) string {
	switch value {
	case "queue":
		return "buffer_all"
	case "replace":
		return "cancel_other"
	case "allow":
		return "allow_all"
	default:
		return "skip"
	}
}

func defaultStringRuntime(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
