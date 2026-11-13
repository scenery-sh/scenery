package compiler

import (
	"testing"
)

func TestDeploymentValueMatchesWrappedTypesRecursively(t *testing.T) {
	tests := []struct {
		value    any
		typeName string
		want     bool
	}{
		{value: "42", typeName: "optional(int)", want: true},
		{value: map[string]any{"not": "an integer"}, typeName: "optional(int)", want: false},
		{value: nil, typeName: "optional(int)", want: false},
		{value: nil, typeName: "nullable(int)", want: true},
		{value: "42", typeName: "nullable(int)", want: true},
		{value: map[string]any{"not": "an integer"}, typeName: "nullable(int)", want: false},
		{value: nil, typeName: "optional(nullable(int))", want: false},
		{value: map[string]any{"not": "an integer"}, typeName: "optional(nullable(int))", want: false},
	}
	for _, test := range tests {
		if got := deploymentValueMatchesType(test.value, test.typeName); got != test.want {
			t.Errorf("deploymentValueMatchesType(%#v, %q) = %t, want %t", test.value, test.typeName, got, test.want)
		}
	}
}
