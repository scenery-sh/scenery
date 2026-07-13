package compiler

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestProviderDescriptorShapeIdentityCoversEveryField(t *testing.T) {
	var shape map[string]any
	if err := json.Unmarshal([]byte(providerSchemaDescriptor), &shape); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(shape))
	for name := range shape {
		got = append(got, name)
	}
	slices.Sort(got)
	want := compilerArtifactJSONFields(reflect.TypeOf(ProviderDescriptor{}))
	if !slices.Equal(got, want) {
		t.Fatalf("descriptor fields = %v, want %v", got, want)
	}
}

func compilerArtifactJSONFields(value reflect.Type) []string {
	fields := []string{}
	for index := 0; index < value.NumField(); index++ {
		field := value.Field(index)
		if field.Anonymous {
			fields = append(fields, compilerArtifactJSONFields(field.Type)...)
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			fields = append(fields, name)
		}
	}
	slices.Sort(fields)
	return fields
}
