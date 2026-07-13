package workspacetx

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestTransactionDescriptorsCoverMetadata(t *testing.T) {
	for descriptor, value := range map[string]any{
		lockDescriptor:    Lock{},
		journalDescriptor: Journal{},
	} {
		var shape map[string]any
		if err := json.Unmarshal([]byte(descriptor), &shape); err != nil {
			t.Fatal(err)
		}
		got := make([]string, 0, len(shape))
		for name := range shape {
			got = append(got, name)
		}
		slices.Sort(got)
		want := jsonFields(reflect.TypeOf(value))
		if !slices.Equal(got, want) {
			t.Fatalf("descriptor fields = %v, want %v", got, want)
		}
	}
}

func jsonFields(value reflect.Type) []string {
	var fields []string
	for index := 0; index < value.NumField(); index++ {
		field := value.Field(index)
		if field.Anonymous {
			fields = append(fields, jsonFields(field.Type)...)
			continue
		}
		if name := strings.Split(field.Tag.Get("json"), ",")[0]; name != "" && name != "-" {
			fields = append(fields, name)
		}
	}
	slices.Sort(fields)
	return fields
}
