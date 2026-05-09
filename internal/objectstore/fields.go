package objectstore

import (
	"fmt"
	"slices"
	"strings"
)

func normalizeFieldType(raw FieldType) (FieldType, error) {
	switch raw {
	case FieldText, FieldRichText, FieldNumber, FieldNumeric, FieldCurrency, FieldBoolean,
		FieldDate, FieldDatetime, FieldUUID, FieldSelect, FieldMultiSelect, FieldRating,
		FieldJSON, FieldRawJSON, FieldFiles, FieldFullName, FieldAddress, FieldEmails,
		FieldPhones, FieldRelation:
		return raw, nil
	default:
		return "", fmt.Errorf("field type %q is not supported", raw)
	}
}

func fieldColumns(name, fieldID string, fieldType FieldType, nullable bool) ([]PhysicalColumn, error) {
	if err := validateName("field", name); err != nil {
		return nil, err
	}
	fieldType, err := normalizeFieldType(fieldType)
	if err != nil {
		return nil, err
	}
	col := func(suffix, sqlType string) PhysicalColumn {
		return PhysicalColumn{
			Name:     physicalColumnName(fieldID, name, suffix),
			Part:     suffix,
			SQLType:  sqlType,
			Nullable: nullable,
		}
	}
	switch fieldType {
	case FieldText, FieldRichText:
		return []PhysicalColumn{col("", "text")}, nil
	case FieldNumber:
		return []PhysicalColumn{col("", "double precision")}, nil
	case FieldNumeric:
		return []PhysicalColumn{col("", "numeric")}, nil
	case FieldCurrency:
		return []PhysicalColumn{
			col("amount", "numeric"),
			col("currency_code", "text"),
		}, nil
	case FieldBoolean:
		return []PhysicalColumn{col("", "boolean")}, nil
	case FieldDate:
		return []PhysicalColumn{col("", "date")}, nil
	case FieldDatetime:
		return []PhysicalColumn{col("", "timestamptz")}, nil
	case FieldUUID, FieldRelation:
		return []PhysicalColumn{col("", "uuid")}, nil
	case FieldSelect:
		return []PhysicalColumn{col("", "text")}, nil
	case FieldMultiSelect:
		return []PhysicalColumn{col("", "text[]")}, nil
	case FieldRating:
		return []PhysicalColumn{col("", "smallint")}, nil
	case FieldJSON, FieldRawJSON, FieldFiles, FieldEmails, FieldPhones:
		return []PhysicalColumn{col("", "jsonb")}, nil
	case FieldFullName:
		return []PhysicalColumn{
			col("first_name", "text"),
			col("last_name", "text"),
		}, nil
	case FieldAddress:
		return []PhysicalColumn{
			col("street", "text"),
			col("city", "text"),
			col("region", "text"),
			col("postal_code", "text"),
			col("country", "text"),
		}, nil
	default:
		return nil, fmt.Errorf("field type %q is not supported", fieldType)
	}
}

func isCompositeField(fieldType FieldType) bool {
	switch fieldType {
	case FieldCurrency, FieldFullName, FieldAddress:
		return true
	default:
		return false
	}
}

func filterOperatorsFor(fieldType FieldType) []string {
	switch fieldType {
	case FieldText, FieldRichText, FieldSelect, FieldUUID, FieldBoolean, FieldDate, FieldDatetime, FieldRating:
		return []string{"eq", "neq", "in", "is_null"}
	case FieldNumber, FieldNumeric:
		return []string{"eq", "neq", "gt", "gte", "lt", "lte", "in", "is_null"}
	case FieldMultiSelect:
		return []string{"eq", "neq", "in", "is_null"}
	default:
		return []string{"eq", "neq", "is_null"}
	}
}

func validateFilterOperator(field *Field, op string) error {
	op = strings.ToLower(strings.TrimSpace(op))
	if op == "contains" {
		switch field.Type {
		case FieldText, FieldRichText, FieldSelect:
			return nil
		default:
			return fmt.Errorf("operator contains is not valid for field %s of type %s", field.Name, field.Type)
		}
	}
	if slices.Contains(filterOperatorsFor(field.Type), op) {
		return nil
	}
	return fmt.Errorf("operator %s is not valid for field %s of type %s", op, field.Name, field.Type)
}

func selectOptionValues(options []FieldOption) map[string]bool {
	out := make(map[string]bool, len(options))
	for _, option := range options {
		if !option.IsArchived {
			out[option.Value] = true
		}
	}
	return out
}
