package scenery

import (
	"errors"
	"testing"
)

const contractTimeRangeValidationProgram = `{"source":"value.end_at <= value.start_at","expression":{"kind":"binary","operator":"<=","left":{"kind":"attribute","source":{"kind":"value"},"name":"end_at"},"right":{"kind":"attribute","source":{"kind":"value"},"name":"start_at"}}}`

func TestContractRecordValidationUsesTypedFieldsAndDeclaredFailure(t *testing.T) {
	start, err := ParseDateTime("2026-07-10T10:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	end, err := ParseDateTime("2026-07-10T09:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	fieldTypes := map[string]string{"start_at": "datetime", "end_at": "datetime"}
	err = ValidateContractRecord(
		map[string]any{"start_at": start, "end_at": end}, fieldTypes,
		contractTimeRangeValidationProgram, "HOUSE_INVALID_TIME_RANGE", "end_at must be later than start_at", "record.run_input.end_at",
	)
	var validation *ContractValidationError
	if !errors.As(err, &validation) || validation.Code != "HOUSE_INVALID_TIME_RANGE" || validation.Path != "record.run_input.end_at" {
		t.Fatalf("validation error = %#v", err)
	}
	if err := ValidateContractRecord(
		map[string]any{"start_at": end, "end_at": start}, fieldTypes,
		contractTimeRangeValidationProgram, "HOUSE_INVALID_TIME_RANGE", "end_at must be later than start_at", "record.run_input.end_at",
	); err != nil {
		t.Fatalf("valid record rejected: %v", err)
	}
}

func TestContractRecordValidationRejectsMalformedCompiledExpression(t *testing.T) {
	err := ValidateContractRecord(map[string]any{"name": "roof"}, map[string]string{"name": "string"}, `{"source":"value.name","expression":{"kind":"network"}}`, "INVALID", "invalid", "record.input.name")
	if err == nil {
		t.Fatal("malformed compiled expression was accepted")
	}
}
