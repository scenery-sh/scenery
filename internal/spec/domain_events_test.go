package spec

import (
	"fmt"
	"testing"
)

func TestDomainEventSchemas(t *testing.T) {
	event, ok := ResourceSourceSchema("event")
	if !ok || !event.Required["payload"] || event.Children["idempotency"].Repeatable {
		t.Fatalf("event shape: %#v", event)
	}
	key := event.Children["idempotency"].Schema.Attributes["key"]
	if !key.Ordered || key.Constraints["reference_root"] != "payload" || key.Type["collection"] != "list" {
		t.Fatalf("idempotency key: %#v", key)
	}
	service, _ := ResourceSourceSchema("service")
	publisher := service.Children["publisher"]
	if !publisher.Repeatable || publisher.Schema.Labels != 1 || !publisher.Schema.Required["event"] ||
		publisher.Schema.Attributes["event"].Type["resource_ref"] != "scenery.event" {
		t.Fatalf("publisher shape: %#v", publisher)
	}
	subscription, _ := ResourceSourceSchema("subscription")
	invoke := subscription.Children["invoke"]
	if invoke.Repeatable || invoke.Schema.Labels != 0 {
		t.Fatalf("invoke shape: %#v", invoke)
	}
	for _, field := range []string{"operation", "execution", "authorization", "pipeline"} {
		if !invoke.Schema.Required[field] || invoke.Schema.Attributes[field].Type["resource_ref"] != "scenery."+field {
			t.Errorf("invalid subscription invoke %s", field)
		}
	}
	for kind, fields := range map[string][]string{
		"scenery.service": {"publisher"}, "scenery.event": {"payload", "external_name", "idempotency"}, "scenery.subscription": {"event", "invoke"},
	} {
		for _, field := range fields {
			if domain, ok := ResourceFieldRevisionDomain(kind, field); !ok || domain != "contract" {
				t.Errorf("%s.%s revision domain = %q", kind, field, domain)
			}
		}
	}
	for i := 2310; i <= 2315; i++ {
		code := fmt.Sprintf("SCN%d", i)
		if _, ok := DiagnosticDefinitionFor(code); !ok || DiagnosticCategory(code) != "execution_and_delivery" {
			t.Errorf("missing event diagnostic %s", code)
		}
	}
}
