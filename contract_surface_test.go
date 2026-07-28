package scenery_test

import (
	"reflect"
	"testing"
	"time"

	scenery "scenery.sh"
	"scenery.sh/internal/contract"
)

// The contract surface is implemented in scenery.sh/internal/contract, but
// generated app code only ever spells it scenery.X. These tests pin that the
// app-facing spelling stays a true alias: same type identity, same methods.

func TestContractFacadeTypeIdentity(t *testing.T) {
	pairs := []struct {
		name   string
		facade any
		leaf   any
	}{
		{"Int", scenery.Int{}, contract.Int{}},
		{"Decimal", scenery.Decimal{}, contract.Decimal{}},
		{"UUID", scenery.UUID(""), contract.UUID("")},
		{"Date", scenery.Date(""), contract.Date("")},
		{"DateTime", scenery.DateTime{}, contract.DateTime{}},
		{"Duration", scenery.Duration{}, contract.Duration{}},
		{"Size", scenery.Size{}, contract.Size{}},
		{"URL", scenery.URL{}, contract.URL{}},
		{"RelativePath", scenery.RelativePath(""), contract.RelativePath("")},
		{"Unit", scenery.Unit{}, contract.Unit{}},
		{"SecretRef", scenery.SecretRef{}, contract.SecretRef{}},
		{"Problem", scenery.Problem{}, contract.Problem{}},
		{"Optional", scenery.Optional[int]{}, contract.Optional[int]{}},
		{"Nullable", scenery.Nullable[int]{}, contract.Nullable[int]{}},
		{"Set", scenery.Set[int]{}, contract.Set[int]{}},
		{"ApprovalToken", scenery.ApprovalToken{}, contract.ApprovalToken{}},
		{"ContractConstraints", scenery.ContractConstraints{}, contract.ContractConstraints{}},
		{"ContractValidationError", scenery.ContractValidationError{}, contract.ContractValidationError{}},
	}
	for _, pair := range pairs {
		facade, leaf := reflect.TypeOf(pair.facade), reflect.TypeOf(pair.leaf)
		if facade != leaf {
			t.Errorf("scenery.%s is %v, want identical to contract.%s (%v)", pair.name, facade, pair.name, leaf)
		}
	}
}

func TestContractFacadeMethodsRemainReachable(t *testing.T) {
	duration, err := scenery.ParseDuration("1h30m")
	if err != nil {
		t.Fatal(err)
	}
	if got := duration.String(); got != "PT1H30M" && got == "" {
		t.Fatalf("Duration.String() = %q", got)
	}
	if duration.Sign() != 1 || duration.Nanoseconds().Sign() != 1 {
		t.Fatalf("Duration.Sign()/Nanoseconds() lost through the facade")
	}
	encoded, err := duration.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var decoded scenery.Duration
	if err := decoded.UnmarshalJSON(encoded); err != nil {
		t.Fatal(err)
	}
	if duration.Cmp(decoded) != 0 {
		t.Fatalf("Duration JSON round trip through the facade changed the value")
	}

	size, err := scenery.ParseSize("10B")
	if err != nil {
		t.Fatal(err)
	}
	if size.Bytes().Int64() != 10 || size.String() != "10" {
		t.Fatalf("Size methods lost through the facade")
	}

	parsed, err := scenery.ParseURL("https://example.com/a")
	if err != nil {
		t.Fatal(err)
	}
	if parsed.String() == "" {
		t.Fatalf("URL.String() lost through the facade")
	}

	moment, err := scenery.ParseDateTime("2026-07-28T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if moment.String() != "2026-07-28T00:00:00Z" {
		t.Fatalf("DateTime.String() = %q", moment.String())
	}
}

func TestContractFacadeValuesCrossTheBoundary(t *testing.T) {
	token := scenery.NewApprovalToken(
		"sha256:"+"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"caller", []string{"scope"}, time.Now().UTC(),
	)
	// A facade-built value must validate in the leaf implementation unchanged.
	if _, err := contract.ApprovalTokenPayload(token); err != nil {
		t.Fatalf("facade-built ApprovalToken rejected by the leaf: %v", err)
	}
	payload, err := scenery.ApprovalTokenPayload(token)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) == 0 {
		t.Fatalf("ApprovalTokenPayload returned no bytes through the facade")
	}

	optional := scenery.Some(42)
	if !optional.Set || optional.Value != 42 {
		t.Fatalf("Some() lost data through the facade")
	}
	if scenery.NoneOf[int]().Set {
		t.Fatalf("NoneOf() should be absent")
	}
	if scenery.ValueOf(7).Null || !scenery.NullOf[int]().Null {
		t.Fatalf("Nullable constructors lost meaning through the facade")
	}
}
