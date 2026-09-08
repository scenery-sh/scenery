package main

import "scenery.sh/internal/machine"

type cliPayloadIdentity = machine.PayloadIdentity

func newCLIPayloadIdentity(kind string) cliPayloadIdentity { return machine.NewPayloadIdentity(kind) }
func withCLIPayloadIdentity(kind string, values map[string]any) map[string]any {
	return machine.WithPayloadIdentity(kind, values)
}
